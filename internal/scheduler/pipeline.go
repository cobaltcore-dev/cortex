// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package scheduler

import (
	"log/slog"
	"sort"

	"github.com/cobaltcore-dev/cortex/internal/conf"
	"github.com/cobaltcore-dev/cortex/internal/db"
	"github.com/cobaltcore-dev/cortex/internal/scheduler/plugins"
	"github.com/cobaltcore-dev/cortex/internal/scheduler/plugins/vmware"
)

// Configuration of steps supported by the scheduler.
// The steps used by the scheduler are defined through the configuration file.
var supportedSteps = []plugins.Step{
	&vmware.VROpsAntiAffinityNoisyProjectsStep{},
	&vmware.AvoidContendedHostsStep{},
}

type Pipeline interface {
	Run(state *plugins.State) ([]string, error)
}

type pipeline struct {
	steps   []plugins.Step
	monitor Monitor
}

// Create a new pipeline with steps contained in the configuration.
func NewPipeline(config conf.SchedulerConfig, database db.DB, monitor Monitor) Pipeline {
	supportedStepsByName := make(map[string]plugins.Step)
	for _, step := range supportedSteps {
		supportedStepsByName[step.GetName()] = step
	}
	steps := []plugins.Step{}
	for _, stepConfig := range config.Steps {
		if step, ok := supportedStepsByName[stepConfig.Name]; ok {
			wrappedStep := monitorStep(step, monitor)
			if err := wrappedStep.Init(database, stepConfig.Options); err != nil {
				panic("failed to initialize pipeline step: " + err.Error())
			}
			steps = append(steps, wrappedStep)
			slog.Info(
				"scheduler: added step",
				"name", stepConfig.Name,
				"options", stepConfig.Options,
			)
		} else {
			panic("unknown pipeline step: " + stepConfig.Name)
		}
	}
	return &pipeline{steps: steps, monitor: monitor}
}

// Evaluate the pipeline and return a list of hosts in order of preference.
func (p *pipeline) Run(state *plugins.State) ([]string, error) {
	if p.monitor.hostNumberInObserver != nil {
		p.monitor.hostNumberInObserver.Observe(float64(len(state.Hosts)))
	}
	for _, step := range p.steps {
		if err := step.Run(state); err != nil {
			return nil, err
		}
	}
	if p.monitor.hostNumberOutObserver != nil {
		p.monitor.hostNumberOutObserver.Observe(float64(len(state.Hosts)))
	}
	// Order the list of hosts by their weights.
	sort.Slice(state.Hosts, func(i, j int) bool {
		hI := state.Hosts[i].ComputeHost
		hJ := state.Hosts[j].ComputeHost
		return state.Weights[hI] > state.Weights[hJ]
	})
	// Flatten to a list of host names.
	hostNames := make([]string, len(state.Hosts))
	for i, host := range state.Hosts {
		hostNames[i] = host.ComputeHost
	}
	return hostNames, nil
}
