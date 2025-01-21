// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package scheduler

import (
	"sort"

	"github.com/cobaltcore-dev/cortex/internal/conf"
	"github.com/cobaltcore-dev/cortex/internal/db"
	"github.com/cobaltcore-dev/cortex/internal/logging"
	"github.com/prometheus/client_golang/prometheus"
)

// Configuration of steps supported by the scheduler.
// The steps used by the scheduler are defined through the configuration file.
var supportedSteps = map[string]func(opts map[string]any, db db.DB) PipelineStep{
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
	Steps                 []PipelineStep
	runTimer              prometheus.Histogram
	hostNumberInObserver  prometheus.Histogram
	hostNumberOutObserver prometheus.Histogram
}

// Create a new pipeline with steps contained in the configuration.
func NewPipeline(config conf.Config, database db.DB) Pipeline {
	steps := []PipelineStep{}
	for _, stepConfig := range config.GetSchedulerConfig().Steps {
		if stepFunc, ok := supportedSteps[stepConfig.Name]; ok {
			step := stepFunc(stepConfig.Options, database)
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
	runTimer := prometheus.NewHistogram(prometheus.HistogramOpts{
		Name:    "cortex_scheduler_pipeline_run_duration_seconds",
		Help:    "Duration of scheduler pipeline run",
		Buckets: prometheus.DefBuckets,
	})
	hostNumberInObserver := prometheus.NewHistogram(prometheus.HistogramOpts{
		Name:    "cortex_scheduler_pipeline_host_number_in",
		Help:    "Number of hosts going into the scheduler pipeline",
		Buckets: prometheus.ExponentialBucketsRange(1, 1000, 10),
	})
	hostNumberOutObserver := prometheus.NewHistogram(prometheus.HistogramOpts{
		Name:    "cortex_scheduler_pipeline_host_number_out",
		Help:    "Number of hosts coming out of the scheduler pipeline",
		Buckets: prometheus.ExponentialBucketsRange(1, 1000, 10),
	})
	prometheus.MustRegister(runTimer, hostNumberInObserver, hostNumberOutObserver)
	return &pipeline{
		Steps:                 steps,
		runTimer:              runTimer,
		hostNumberInObserver:  hostNumberInObserver,
		hostNumberOutObserver: hostNumberOutObserver,
	}
}

// Evaluate the pipeline and return a list of hosts in order of preference.
func (p *pipeline) Run(state *pipelineState) ([]string, error) {
	if p.runTimer != nil {
		timer := prometheus.NewTimer(p.runTimer)
		defer timer.ObserveDuration()
	}
	if p.hostNumberInObserver != nil {
		p.hostNumberInObserver.Observe(float64(len(state.Hosts)))
	}
	for _, step := range p.Steps {
		if err := step.Run(state); err != nil {
			return nil, err
		}
	}
	if p.hostNumberOutObserver != nil {
		p.hostNumberOutObserver.Observe(float64(len(state.Hosts)))
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
