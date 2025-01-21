// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package scheduler

import (
	"github.com/cobaltcore-dev/cortex/internal/db"
	"github.com/cobaltcore-dev/cortex/internal/features"
	"github.com/cobaltcore-dev/cortex/internal/logging"
	"github.com/prometheus/client_golang/prometheus"
)

type avoidContendedHostsStep struct {
	DB                        db.DB
	AvgCPUContentionThreshold any
	MaxCPUContentionThreshold any
	runCounter                prometheus.Counter
	runTimer                  prometheus.Histogram
}

func NewAvoidContendedHostsStep(opts map[string]any, db db.DB) PipelineStep {
	runCounter := prometheus.NewCounter(prometheus.CounterOpts{
		Name: "cortex_scheduler_avoid_contended_hosts_runs",
		Help: "Total number of avoid contended hosts runs",
	})
	runTimer := prometheus.NewHistogram(prometheus.HistogramOpts{
		Name:    "cortex_scheduler_avoid_contended_hosts_duration_seconds",
		Help:    "Duration of avoid contended hosts run",
		Buckets: prometheus.DefBuckets,
	})
	prometheus.MustRegister(runCounter, runTimer)
	return &avoidContendedHostsStep{
		DB:                        db,
		AvgCPUContentionThreshold: opts["avgCPUContentionThreshold"],
		MaxCPUContentionThreshold: opts["maxCPUContentionThreshold"],
		runCounter:                runCounter,
		runTimer:                  runTimer,
	}
}

// Downvote hosts that are highly contended.
func (s *avoidContendedHostsStep) Run(state *pipelineState) error {
	if s.runCounter != nil {
		s.runCounter.Inc()
	}
	if s.runTimer != nil {
		timer := prometheus.NewTimer(s.runTimer)
		defer timer.ObserveDuration()
	}

	logging.Log.Info("scheduler: contention - avoid contended hosts")

	var highlyContendedHosts []features.VROpsHostsystemContention
	if err := s.DB.Get().
		Model(&highlyContendedHosts).
		Where("avg_cpu_contention > ?", s.AvgCPUContentionThreshold).
		WhereOr("max_cpu_contention > ?", s.MaxCPUContentionThreshold).
		Select(); err != nil {
		return err
	}
	var hostsByName = make(map[string]features.VROpsHostsystemContention)
	for _, h := range highlyContendedHosts {
		hostsByName[h.ComputeHost] = h
	}
	for i := range state.Hosts {
		if h, ok := hostsByName[state.Hosts[i].ComputeHost]; ok {
			state.Weights[state.Hosts[i].ComputeHost] = 0.0
			logging.Log.Info(
				"scheduler: downvoting host",
				"host", h.ComputeHost,
				"avgContention", h.AvgCPUContention,
				"maxContention", h.MaxCPUContention,
			)
		}
	}
	return nil
}
