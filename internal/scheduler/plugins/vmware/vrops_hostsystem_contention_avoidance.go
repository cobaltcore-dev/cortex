// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package vmware

import (
	"errors"

	"github.com/cobaltcore-dev/cortex/internal/db"
	"github.com/cobaltcore-dev/cortex/internal/features/plugins/vmware"
	"github.com/cobaltcore-dev/cortex/internal/scheduler/plugins"
)

type AvoidContendedHostsStep struct {
	DB                        db.DB
	AvgCPUContentionThreshold any // Can be float64 or int
	MaxCPUContentionThreshold any // Can be float64 or int
}

func (s *AvoidContendedHostsStep) GetName() string {
	return "vrops_avoid_contended_hosts"
}

func (s *AvoidContendedHostsStep) Init(db db.DB, opts map[string]any) error {
	s.DB = db
	avgCPUThreshold, ok := opts["avgCPUContentionThreshold"]
	if !ok {
		return errors.New("missing avgCPUContentionThreshold")
	}
	s.AvgCPUContentionThreshold = avgCPUThreshold
	maxCPUThreshold, ok := opts["maxCPUContentionThreshold"]
	if !ok {
		return errors.New("missing maxCPUContentionThreshold")
	}
	s.MaxCPUContentionThreshold = maxCPUThreshold
	return nil
}

// Downvote hosts that are highly contended.
func (s *AvoidContendedHostsStep) Run(state *plugins.State) error {
	var highlyContendedHosts []vmware.VROpsHostsystemContention
	if err := s.DB.Get().
		Model(&highlyContendedHosts).
		Where("avg_cpu_contention > ?", s.AvgCPUContentionThreshold).
		WhereOr("max_cpu_contention > ?", s.MaxCPUContentionThreshold).
		Select(); err != nil {
		return err
	}
	var hostsByName = make(map[string]vmware.VROpsHostsystemContention)
	for _, h := range highlyContendedHosts {
		hostsByName[h.ComputeHost] = h
	}
	for i := range state.Hosts {
		if _, ok := hostsByName[state.Hosts[i].ComputeHost]; ok {
			state.Weights[state.Hosts[i].ComputeHost] = 0.0
		}
	}
	return nil
}
