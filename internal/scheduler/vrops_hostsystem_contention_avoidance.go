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
	runTimer                  prometheus.Observer
	weightModObserver         prometheus.Observer
}

func NewAvoidContendedHostsStep(opts map[string]any, db db.DB, m Monitor) PipelineStep {
	stepName := "vrops_avoid_contended_hosts"
	var runTimer prometheus.Observer
	if m.stepRunTimer != nil {
		runTimer = m.stepRunTimer.WithLabelValues(stepName)
	}
	var weightModObserver prometheus.Observer
	if m.stepWeightModObserver != nil {
		weightModObserver = m.stepWeightModObserver.WithLabelValues(stepName)
	}
	return &avoidContendedHostsStep{
		DB:                        db,
		AvgCPUContentionThreshold: opts["avgCPUContentionThreshold"],
		MaxCPUContentionThreshold: opts["maxCPUContentionThreshold"],
		runTimer:                  runTimer,
		weightModObserver:         weightModObserver,
	}
}

// Downvote hosts that are highly contended.
func (s *avoidContendedHostsStep) Run(state *pipelineState) error {
	if s.runTimer != nil {
		timer := prometheus.NewTimer(s.runTimer)
		defer timer.ObserveDuration()
	}

	logging.Log.Debug("scheduler: contention - avoid contended hosts")

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
	var modifiedWeights = 0
	for i := range state.Hosts {
		if h, ok := hostsByName[state.Hosts[i].ComputeHost]; ok {
			state.Weights[state.Hosts[i].ComputeHost] = 0.0
			modifiedWeights++
			logging.Log.Debug(
				"scheduler: downvoting host",
				"host", h.ComputeHost,
				"avgContention", h.AvgCPUContention,
				"maxContention", h.MaxCPUContention,
			)
		}
	}
	if s.weightModObserver != nil {
		s.weightModObserver.Observe(float64(modifiedWeights))
	}
	return nil
}
