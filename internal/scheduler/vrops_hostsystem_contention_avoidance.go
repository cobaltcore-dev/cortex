// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package scheduler

import (
	"github.com/cobaltcore-dev/cortex/internal/db"
	"github.com/cobaltcore-dev/cortex/internal/features"
	"github.com/cobaltcore-dev/cortex/internal/logging"
)

type avoidContendedHostsStep struct {
	DB                        db.DB
	AvgCPUContentionThreshold any
	MaxCPUContentionThreshold any
}

func NewAvoidContendedHostsStep(opts map[string]any, db db.DB) PipelineStep {
	return &avoidContendedHostsStep{
		DB:                        db,
		AvgCPUContentionThreshold: opts["avgCPUContentionThreshold"],
		MaxCPUContentionThreshold: opts["maxCPUContentionThreshold"],
	}
}

// Downvote hosts that are highly contended.
func (s *avoidContendedHostsStep) Run(state *pipelineState) error {
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
