// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package nova

import (
	"log/slog"
	"maps"
	"math"
	"slices"
	"sort"
	"sync"
	"time"

	"github.com/cobaltcore-dev/cortex/internal/conf"
	"github.com/cobaltcore-dev/cortex/internal/db"
	"github.com/cobaltcore-dev/cortex/internal/mqtt"
	"github.com/cobaltcore-dev/cortex/internal/scheduler"
	"github.com/cobaltcore-dev/cortex/internal/scheduler/nova/api"
	"github.com/cobaltcore-dev/cortex/internal/scheduler/nova/plugins"
	"github.com/cobaltcore-dev/cortex/internal/scheduler/nova/plugins/kvm"
	"github.com/cobaltcore-dev/cortex/internal/scheduler/nova/plugins/shared"
	"github.com/cobaltcore-dev/cortex/internal/scheduler/nova/plugins/vmware"
)

// Configuration of steps supported by the scheduler.
// The steps actually used by the scheduler are defined through the configuration file.
var SupportedSteps = []plugins.Step{
	// VMware-specific steps
	&vmware.AntiAffinityNoisyProjectsStep{},
	&vmware.AvoidLongTermContendedHostsStep{},
	&vmware.AvoidShortTermContendedHostsStep{},
	// KVM-specific steps
	&kvm.AvoidOverloadedHostsCPUStep{},
	&kvm.AvoidOverloadedHostsMemoryStep{},
	// Shared steps
	&shared.FlavorBinpackingStep{},
	&shared.ResourceBalancingStep{},
}

// Pipeline of scheduler steps.
type pipeline struct {
	// The activation function to use when combining the
	// results of the scheduler steps.
	scheduler.ActivationFunction
	// The parallelizable order in which scheduler steps are executed.
	executionOrder [][]plugins.Step
	// The order in which scheduler steps are applied, by their step name.
	applicationOrder []string
	// Monitor to observe the pipeline.
	monitor Monitor
	// MQTT client to publish mqtt data.
	mqttClient mqtt.Client
}

// Create a new pipeline with steps contained in the configuration.
func NewPipeline(
	supportedSteps []plugins.Step,
	config conf.SchedulerConfig,
	database db.DB,
	monitor Monitor,
	mqttClient mqtt.Client,
) api.Pipeline {

	supportedStepsByName := make(map[string]plugins.Step)
	for _, step := range supportedSteps {
		supportedStepsByName[step.GetName()] = step
	}

	// Load all steps from the configuration.
	steps := []plugins.Step{}
	applicationOrder := []string{}
	for _, stepConfig := range config.Nova.Plugins {
		step, ok := supportedStepsByName[stepConfig.Name]
		if !ok {
			panic("unknown pipeline step: " + stepConfig.Name)
		}
		// Scope the step.
		step = scopeStep(step, stepConfig.Scope)
		// Monitor the step execution.
		step = monitorStep(step, monitor)
		// Validate the step during execution.
		step = validateStep(step, stepConfig.DisabledValidations)
		if err := step.Init(database, stepConfig.Options); err != nil {
			panic("failed to initialize pipeline step: " + err.Error())
		}
		steps = append(steps, step)
		applicationOrder = append(applicationOrder, stepConfig.Name)
		slog.Info(
			"scheduler: added step",
			"name", stepConfig.Name,
			"options", stepConfig.Options,
		)
	}

	return &pipeline{
		// All steps can be run in parallel.
		executionOrder:   [][]plugins.Step{steps},
		applicationOrder: applicationOrder,
		monitor:          monitor,
		mqttClient:       mqttClient,
	}
}

// Get a logger that can be followed from Nova to trace bugs.
func (p *pipeline) logger(request api.Request) *slog.Logger {
	ctx := request.GetContext()
	return slog.With(
		slog.String("greq", ctx.GlobalRequestID),
		slog.String("req", ctx.RequestID),
		slog.String("user", ctx.UserID),
		slog.String("project", ctx.ProjectID),
	)
}

// Execute the scheduler steps in groups of the execution order.
// The steps are run in parallel.
func (p *pipeline) runSteps(log *slog.Logger, request api.Request) map[string]map[string]float64 {
	var lock sync.Mutex
	activationsByStep := map[string]map[string]float64{}
	for _, steps := range p.executionOrder {
		var wg sync.WaitGroup
		for _, step := range steps {
			wg.Add(1)
			go func() {
				defer wg.Done()
				log.Info("scheduler: running step", "name", step.GetName())
				result, err := step.Run(log, request)
				log.Info("scheduler: finished step", "name", step.GetName())
				if err != nil {
					log.Error("scheduler: failed to run step", "error", err)
					return
				}
				lock.Lock()
				defer lock.Unlock()
				activationsByStep[step.GetName()] = result.Activations
			}()
		}
		wg.Wait()
	}
	return activationsByStep
}

// Apply an initial weight to the hosts.
//
// Context:
// Nova may give us very large (positive/negative) weights such as
// -99,000 or 99,000. We want to respect these values, but still adjust them
// to a meaningful value. If Nova really doesn't want us to run on a host, it
// should run a filter instead of setting a weight.
func (p *pipeline) normalizeNovaWeights(weights map[string]float64) map[string]float64 {
	normalizedWeights := make(map[string]float64, len(weights))
	for hostname, weight := range weights {
		normalizedWeights[hostname] = math.Tanh(weight)
	}
	return normalizedWeights
}

// Apply the step weights to the input weights.
func (p *pipeline) applyStepWeights(
	log *slog.Logger,
	stepWeights map[string]map[string]float64,
	inWeights map[string]float64,
) map[string]float64 {
	// Copy to avoid modifying the original weights.
	outWeights := make(map[string]float64, len(inWeights))
	maps.Copy(outWeights, inWeights)

	// Apply all activations in the strict order defined by the configuration.
	for _, stepName := range p.applicationOrder {
		stepActivations, ok := stepWeights[stepName]
		if !ok {
			log.Error("scheduler: missing activations for step", "name", stepName)
			continue
		}
		outWeights = p.Apply(outWeights, stepActivations)
	}
	return outWeights
}

// Sort the hosts by their weights.
func (s *pipeline) sortHostsByWeights(weights map[string]float64) []string {
	// Sort the hosts (keys) by their weights.
	hosts := slices.Collect(maps.Keys(weights))
	sort.Slice(hosts, func(i, j int) bool {
		return weights[hosts[i]] > weights[hosts[j]]
	})
	return hosts
}

// Evaluate the pipeline and return a list of hosts in order of preference.
func (p *pipeline) Run(request api.Request) ([]string, error) {
	traceLog := p.logger(request)
	hostsIn := request.GetHosts()
	traceLog.Info("scheduler: starting pipeline", "hosts", hostsIn)

	// Get weights from the scheduler steps, apply them to the Nova weights, and
	// sort the hosts by their weights. The nova weights are normalized before
	// applying the step weights.
	stepWeights := p.runSteps(traceLog, request)
	traceLog.Info("scheduler: finished pipeline")
	novaWeights := request.GetWeights()
	inWeights := p.normalizeNovaWeights(novaWeights)
	traceLog.Info("scheduler: input weights", "weights", inWeights)
	outWeights := p.applyStepWeights(traceLog, stepWeights, inWeights)
	traceLog.Info("scheduler: output weights", "weights", outWeights)
	hosts := p.sortHostsByWeights(outWeights)
	traceLog.Info("scheduler: sorted hosts", "hosts", hosts)

	// Collect some metrics about the pipeline execution.
	go p.monitor.observePipelineResult(request, hosts)

	// Publish telemetry information about the scheduling to an mqtt broker.
	// In this way, other services can connect and record the scheduler
	// behavior over a longer time, or react to the scheduling decision.
	go p.mqttClient.Publish("cortex/scheduler/nova/pipeline/finished", map[string]any{
		"time":    time.Now().Unix(),
		"request": request,
		"order":   p.applicationOrder,
		"in":      inWeights,
		"steps":   stepWeights,
		"out":     outWeights,
	})

	return hosts, nil
}
