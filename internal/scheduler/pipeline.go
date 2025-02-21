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

	"github.com/cobaltcore-dev/cortex/internal/conf"
	"github.com/cobaltcore-dev/cortex/internal/db"
	"github.com/cobaltcore-dev/cortex/internal/scheduler/plugins"
	"github.com/cobaltcore-dev/cortex/internal/scheduler/plugins/vmware"
)

// Configuration of steps supported by the scheduler.
// The steps actually used by the scheduler are defined through the configuration file.
var supportedSteps = []plugins.Step{
	&vmware.AntiAffinityNoisyProjectsStep{},
	&vmware.AvoidContendedHostsStep{},
}

// Sequence of scheduler steps that are executed in parallel.
type Pipeline interface {
	// Evaluate the pipeline and return a list of hosts in order of preference.
	Run(scenario plugins.Scenario, novaWeights map[string]float64) ([]string, error)
}

// Pipeline of scheduler steps.
type pipeline struct {
	// The activation function to use when combining the
	// results of the scheduler steps.
	plugins.ActivationFunction
	// The parallelizable order in which scheduler steps are executed.
	executionOrder [][]plugins.Step
	// The order in which scheduler steps are applied, by their step name.
	applicationOrder []string
	// Monitor to observe the pipeline.
	monitor Monitor
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

	return &pipeline{
		// All steps can be run in parallel.
		executionOrder:   [][]plugins.Step{steps},
		applicationOrder: applicationOrder,
		monitor:          monitor,
	}
}

// Evaluate the pipeline and return a list of hosts in order of preference.
func (p *pipeline) Run(scenario plugins.Scenario, novaWeights map[string]float64) ([]string, error) {
	if p.monitor.hostNumberInObserver != nil {
		p.monitor.hostNumberInObserver.Observe(float64(len(scenario.GetHosts())))
	}

	// Execute the scheduler steps in groups of the execution order.
	var activationsByStep sync.Map
	for _, steps := range p.executionOrder {
		var wg sync.WaitGroup
		for _, step := range steps {
			wg.Add(1)
			go func(step plugins.Step) {
				defer wg.Done()
				activations, err := step.Run(scenario)
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
	var outWeights = make(map[string]float64)
	for hostname, weight := range novaWeights {
		outWeights[hostname] = math.Tanh(weight)
	}

	// Apply all activations in the strict order defined by the configuration.
	for _, stepName := range p.applicationOrder {
		stepActivations, ok := activationsByStep.Load(stepName)
		if !ok {
			slog.Error("scheduler: missing activations for step", "name", stepName)
			continue
		}
		outWeights = p.ActivationFunction.Apply(outWeights, stepActivations.(map[string]float64))
	}

	if p.monitor.hostNumberOutObserver != nil {
		p.monitor.hostNumberOutObserver.Observe(float64(len(scenario.GetHosts())))
	}

	// Sort the hosts (keys) by their weights.
	hosts := slices.Collect(maps.Keys(outWeights))
	sort.Slice(hosts, func(i, j int) bool {
		return outWeights[hosts[i]] > outWeights[hosts[j]]
	})
	return hosts, nil
}
