// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package scheduler

import (
	"log/slog"

	"github.com/cobaltcore-dev/cortex/internal/db"
	"github.com/cobaltcore-dev/cortex/internal/monitoring"
	"github.com/cobaltcore-dev/cortex/internal/scheduler/plugins"
	"github.com/prometheus/client_golang/prometheus"
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

func (s *StepMonitor[S]) Init(db db.DB, opts map[string]any) error {
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
func (s *StepMonitor[S]) Run(state *plugins.State) error {
	stepName := s.GetName()
	slog.Debug("scheduler: running step", "name", stepName)
	if s.runTimer != nil {
		timer := prometheus.NewTimer(s.runTimer)
		defer timer.ObserveDuration()
	}

	hostsIn := make(map[string]struct{})
	for _, h := range state.Hosts {
		hostsIn[h.ComputeHost] = struct{}{}
	}
	weightsIn := make(map[string]float64)
	for k, v := range state.Weights {
		weightsIn[k] = v
	}
	defer func() {
		// Observe the removed hosts in the state.
		var removedHosts = 0
		for _, h := range state.Hosts {
			if _, ok := hostsIn[h.ComputeHost]; !ok {
				slog.Info(
					"scheduler: removed host",
					"step", stepName,
					"host", h.ComputeHost,
				)
				removedHosts++
			}
		}
		if s.removedHostsObserver != nil {
			s.removedHostsObserver.Observe(float64(removedHosts))
		}

		// Observe the changes to the weights.
		var modifiedWeights = 0
		for k, v := range state.Weights {
			if weightsIn[k] != v {
				slog.Info(
					"scheduler: weight change",
					"step", stepName,
					"host", k,
					"before", weightsIn[k],
					"after", v,
				)
				modifiedWeights++
			}
		}
		if s.weightModObserver != nil {
			s.weightModObserver.Observe(float64(modifiedWeights))
		}
	}()

	return s.Step.Run(state)
}
