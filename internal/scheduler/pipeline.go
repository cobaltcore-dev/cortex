// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package scheduler

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
	"github.com/cobaltcore-dev/cortex/internal/scheduler/api"
	"github.com/cobaltcore-dev/cortex/internal/scheduler/plugins"
	"github.com/cobaltcore-dev/cortex/internal/scheduler/plugins/kvm"
	"github.com/cobaltcore-dev/cortex/internal/scheduler/plugins/shared"
	"github.com/cobaltcore-dev/cortex/internal/scheduler/plugins/vmware"
)

// Configuration of steps supported by the scheduler.
// The steps actually used by the scheduler are defined through the configuration file.
var supportedSteps = []plugins.Step{
	// VMware-specific steps
	&vmware.AntiAffinityNoisyProjectsStep{},
	&vmware.AvoidContendedHostsStep{},
	// KVM-specific steps
	&kvm.AvoidOverloadedHostsCPUStep{},
	&kvm.AvoidOverloadedHostsMemoryStep{},
	// Shared steps
	&shared.FlavorBinpackingStep{},
}

// Pipeline of scheduler steps.
type Pipeline struct {
	// The activation function to use when combining the
	// results of the scheduler steps.
	plugins.ActivationFunction
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
func NewPipeline(config conf.SchedulerConfig, database db.DB, monitor Monitor) Pipeline {
	supportedStepsByName := make(map[string]plugins.Step)
	for _, step := range supportedSteps {
		supportedStepsByName[step.GetName()] = step
	}

	// Load all steps from the configuration.
	steps := []plugins.Step{}
	applicationOrder := []string{}
	for _, stepConfig := range config.Steps {
		step, ok := supportedStepsByName[stepConfig.Name]
		if !ok {
			panic("unknown pipeline step: " + stepConfig.Name)
		}
		wrappedStep := monitorStep(step, monitor)
		if err := wrappedStep.Init(database, stepConfig.Options); err != nil {
			panic("failed to initialize pipeline step: " + err.Error())
		}
		steps = append(steps, wrappedStep)
		applicationOrder = append(applicationOrder, stepConfig.Name)
		slog.Info(
			"scheduler: added step",
			"name", stepConfig.Name,
			"options", stepConfig.Options,
		)
	}

	return Pipeline{
		// All steps can be run in parallel.
		executionOrder:   [][]plugins.Step{steps},
		applicationOrder: applicationOrder,
		monitor:          monitor,
		mqttClient:       mqtt.NewClient(),
	}
}

// Evaluate the pipeline and return a list of hosts in order of preference.
func (p *Pipeline) Run(request api.Request, novaWeights map[string]float64) ([]string, error) {
	if p.monitor.hostNumberInObserver != nil {
		p.monitor.hostNumberInObserver.Observe(float64(len(request.Hosts)))
	}

	// Execute the scheduler steps in groups of the execution order.
	var activationsByStep sync.Map
	for _, steps := range p.executionOrder {
		var wg sync.WaitGroup
		for _, step := range steps {
			wg.Add(1)
			go func(step plugins.Step) {
				defer wg.Done()
				activations, err := step.Run(request)
				if err != nil {
					slog.Error("scheduler: failed to run step", "error", err)
					return
				}
				activationsByStep.Store(step.GetName(), activations)
			}(step)
		}
		wg.Wait()
	}

	// Nova may give us very large (positive/negative) weights such as
	// -99,000 or 99,000. We want to respect these values, but still adjust them
	// to a meaningful value. If Nova really doesn't want us to run on a host, it
	// should run a filter instead of setting a weight.
	inWeights := map[string]float64{}
	for hostname, weight := range novaWeights {
		inWeights[hostname] = math.Tanh(weight)
	}

	// Retrieve the step weights from the concurrency-safe map.
	stepWeights := map[string]map[string]float64{}
	activationsByStep.Range(func(key, value interface{}) bool {
		stepName := key.(string)
		activations := value.(map[string]float64)
		stepWeights[stepName] = activations
		return true
	})

	// Copy to avoid modifying the original weights.
	outWeights := make(map[string]float64, len(inWeights))
	maps.Copy(outWeights, inWeights)

	// Apply all activations in the strict order defined by the configuration.
	for _, stepName := range p.applicationOrder {
		stepActivations, ok := stepWeights[stepName]
		if !ok {
			slog.Error("scheduler: missing activations for step", "name", stepName)
			continue
		}
		outWeights = p.ActivationFunction.Apply(outWeights, stepActivations)
	}

	if p.monitor.hostNumberOutObserver != nil {
		p.monitor.hostNumberOutObserver.Observe(float64(len(outWeights)))
	}

	// Sort the hosts (keys) by their weights.
	hosts := slices.Collect(maps.Keys(outWeights))
	sort.Slice(hosts, func(i, j int) bool {
		return outWeights[hosts[i]] > outWeights[hosts[j]]
	})

	// Publish telemetry information about the scheduling to an mqtt broker.
	// In this way, other services can connect and record the scheduler
	// behavior over a longer time, or react to the scheduling decision.
	go p.mqttClient.Publish("cortex/scheduler/pipeline/finished", map[string]any{
		"time":    time.Now().Unix(),
		"request": request,
		"order":   p.applicationOrder,
		"in":      inWeights,
		"steps":   stepWeights,
		"out":     outWeights,
	})

	return hosts, nil
}
