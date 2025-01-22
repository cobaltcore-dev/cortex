// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package scheduler

import (
	"sort"

	"github.com/cobaltcore-dev/cortex/internal/conf"
	"github.com/cobaltcore-dev/cortex/internal/db"
	"github.com/cobaltcore-dev/cortex/internal/logging"
)

// Configuration of steps supported by the scheduler.
// The steps used by the scheduler are defined through the configuration file.
var supportedSteps = map[string]func(map[string]any, db.DB, monitor) PipelineStep{
	"vrops_anti_affinity_noisy_projects": NewVROpsAntiAffinityNoisyProjectsStep,
	"vrops_avoid_contended_hosts":        NewAvoidContendedHostsStep,
}

type pipelineStateSpec struct {
	ProjectID string
}

type pipelineStateHost struct {
	// Name of the Nova compute host, e.g. nova-compute-bb123.
	ComputeHost string
	// Name of the hypervisor hostname, e.g. domain-c123.<uuid>
	HypervisorHostname string
	// Status of the host, e.g. "enabled".
	Status string
}

// State passed through the pipeline.
// Each step in the pipeline can modify the hosts or their weights.
type pipelineState struct {
	Spec    pipelineStateSpec
	Hosts   []pipelineStateHost
	Weights map[string]float64
}

type PipelineStep interface {
	Run(state *pipelineState) error
}

type Pipeline interface {
	Run(state *pipelineState) ([]string, error)
}

type pipeline struct {
	Steps   []PipelineStep
	monitor monitor
}

// Create a new pipeline with steps contained in the configuration.
func NewPipeline(config conf.Config, database db.DB, monitor monitor) Pipeline {
	steps := []PipelineStep{}
	for _, stepConfig := range config.GetSchedulerConfig().Steps {
		if stepFunc, ok := supportedSteps[stepConfig.Name]; ok {
			step := stepFunc(stepConfig.Options, database, monitor)
			steps = append(steps, step)
			logging.Log.Info(
				"scheduler: added step",
				"name", stepConfig.Name,
				"options", stepConfig.Options,
			)
		} else {
			panic("unknown pipeline step: " + stepConfig.Name)
		}
	}
	return &pipeline{Steps: steps, monitor: monitor}
}

// Evaluate the pipeline and return a list of hosts in order of preference.
func (p *pipeline) Run(state *pipelineState) ([]string, error) {
	if p.monitor.hostNumberInObserver != nil {
		p.monitor.hostNumberInObserver.Observe(float64(len(state.Hosts)))
	}
	for _, step := range p.Steps {
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
