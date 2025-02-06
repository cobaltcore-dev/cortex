// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package scheduler

import (
	"log/slog"

	"github.com/cobaltcore-dev/cortex/internal/db"
	"github.com/cobaltcore-dev/cortex/internal/monitoring"
	"github.com/cobaltcore-dev/cortex/internal/scheduler/plugins"
	"github.com/prometheus/client_golang/prometheus"
	"gopkg.in/yaml.v2"
)

type Monitor struct {
	stepRunTimer             *prometheus.HistogramVec
	stepWeightModObserver    *prometheus.HistogramVec
	stepRemovedHostsObserver *prometheus.HistogramVec
	apiRequestsTimer         *prometheus.HistogramVec
	pipelineRunTimer         prometheus.Histogram
	hostNumberInObserver     prometheus.Histogram
	hostNumberOutObserver    prometheus.Histogram
}

func NewSchedulerMonitor(registry *monitoring.Registry) Monitor {
	stepRunTimer := prometheus.NewHistogramVec(prometheus.HistogramOpts{
		Name:    "cortex_scheduler_pipeline_step_run_duration_seconds",
		Help:    "Duration of scheduler pipeline step run",
		Buckets: prometheus.DefBuckets,
	}, []string{"step"})
	stepWeightModObserver := prometheus.NewHistogramVec(prometheus.HistogramOpts{
		Name:    "cortex_scheduler_pipeline_step_weight_modification",
		Help:    "Modification of host weight by scheduler pipeline step",
		Buckets: prometheus.ExponentialBucketsRange(1, 1000, 10),
	}, []string{"step"})
	stepRemovedHostsObserver := prometheus.NewHistogramVec(prometheus.HistogramOpts{
		Name:    "cortex_scheduler_pipeline_step_removed_hosts",
		Help:    "Number of hosts removed by scheduler pipeline step",
		Buckets: prometheus.ExponentialBucketsRange(1, 1000, 10),
	}, []string{"step"})
	apiRequestsTimer := prometheus.NewHistogramVec(prometheus.HistogramOpts{
		Name:    "cortex_scheduler_api_request_duration_seconds",
		Help:    "Duration of API requests",
		Buckets: prometheus.DefBuckets,
	}, []string{"method", "path", "status", "error"})
	pipelineRunTimer := prometheus.NewHistogram(prometheus.HistogramOpts{
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
	registry.MustRegister(
		stepRunTimer,
		stepWeightModObserver,
		stepRemovedHostsObserver,
		apiRequestsTimer,
		pipelineRunTimer,
		hostNumberInObserver,
		hostNumberOutObserver,
	)
	return Monitor{
		stepRunTimer:             stepRunTimer,
		stepWeightModObserver:    stepWeightModObserver,
		stepRemovedHostsObserver: stepRemovedHostsObserver,
		apiRequestsTimer:         apiRequestsTimer,
		pipelineRunTimer:         pipelineRunTimer,
		hostNumberInObserver:     hostNumberInObserver,
		hostNumberOutObserver:    hostNumberOutObserver,
	}
}

// Wraps a scheduler step to monitor its execution.
type StepMonitor[S plugins.Step] struct {
	// The wrapped scheduler step to monitor.
	Step S
	// A timer to measure how long the step takes to run.
	runTimer prometheus.Observer
	// A metric to observe how much the step modifies the weights of the hosts.
	weightModObserver prometheus.Observer
	// A metric to observe how many hosts are removed from the state.
	removedHostsObserver prometheus.Observer
}

func (s *StepMonitor[S]) GetName() string {
	// Get the name of the wrapped step.
	return s.Step.GetName()
}

func (s *StepMonitor[S]) Init(db db.DB, opts yaml.MapSlice) error {
	// Configure the wrapped step.
	return s.Step.Init(db, opts)
}

func monitorStep[S plugins.Step](step S, m Monitor) *StepMonitor[S] {
	stepName := step.GetName()
	var runTimer prometheus.Observer
	if m.stepRunTimer != nil {
		runTimer = m.stepRunTimer.WithLabelValues(stepName)
	}
	var weightModObserver prometheus.Observer
	if m.stepWeightModObserver != nil {
		weightModObserver = m.stepWeightModObserver.WithLabelValues(stepName)
	}
	var removedHostsObserver prometheus.Observer
	if m.stepRemovedHostsObserver != nil {
		removedHostsObserver = m.stepRemovedHostsObserver.WithLabelValues(stepName)
	}
	return &StepMonitor[S]{
		Step:                 step,
		runTimer:             runTimer,
		weightModObserver:    weightModObserver,
		removedHostsObserver: removedHostsObserver,
	}
}

// Run the step and observe its execution.
func (s *StepMonitor[S]) Run(scenario plugins.Scenario) (map[string]float64, error) {
	stepName := s.GetName()

	slog.Info("scheduler: running step", "name", stepName)
	defer slog.Info("scheduler: finished step", "name", stepName)

	if s.runTimer != nil {
		timer := prometheus.NewTimer(s.runTimer)
		defer timer.ObserveDuration()
	}

	weights, err := s.Step.Run(scenario)
	if err != nil {
		return nil, err
	}

	// Observe how much the step modifies the weights of the hosts.
	if s.weightModObserver != nil {
		for _, weight := range weights {
			s.weightModObserver.Observe(weight)
			if weight != 0.0 {
				slog.Info("scheduler: modified host weight", "name", stepName, "weight", weight)
			}
		}
	}

	// Observe how many hosts are removed from the state.
	hostsInScenario := make(map[string]struct{})
	for _, host := range scenario.GetHosts() {
		hostsInScenario[host.GetComputeHost()] = struct{}{}
	}
	nHostsRemoved := len(hostsInScenario) - len(weights)
	if nHostsRemoved < 0 {
		slog.Info("scheduler: removed hosts", "name", stepName, "count", nHostsRemoved)
	}
	if s.removedHostsObserver != nil {
		s.removedHostsObserver.Observe(float64(nHostsRemoved))
	}

	return weights, nil
}
