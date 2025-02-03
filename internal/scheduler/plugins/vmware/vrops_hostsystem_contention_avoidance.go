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
	AvgCPUContentionThreshold float64
	MaxCPUContentionThreshold float64
	ActivationOnHit           float64
}

func (s *AvoidContendedHostsStep) GetName() string {
	return "vrops_avoid_contended_hosts"
}

func (s *AvoidContendedHostsStep) Init(db db.DB, opts map[string]any) error {
	s.DB = db
	avgCPUContentionThreshold, ok := opts["avgCPUContentionThreshold"]
	if !ok {
		return errors.New("missing avgCPUContentionThreshold")
	}
	if avgCPUContentionThresholdInt, okInt := avgCPUContentionThreshold.(int); okInt {
		avgCPUContentionThreshold = float64(avgCPUContentionThresholdInt)
	}
	s.AvgCPUContentionThreshold = avgCPUContentionThreshold.(float64)

	maxCPUContentionThreshold, ok := opts["maxCPUContentionThreshold"]
	if !ok {
		return errors.New("missing maxCPUContentionThreshold")
	}
	if maxCPUContentionThresholdInt, okInt := maxCPUContentionThreshold.(int); okInt {
		maxCPUContentionThreshold = float64(maxCPUContentionThresholdInt)
	}
	s.MaxCPUContentionThreshold = maxCPUContentionThreshold.(float64)

	activationOnHit, ok := opts["activationOnHit"]
	if !ok {
		return errors.New("missing activationOnHit")
	}
	if activationOnHitInt, okInt := activationOnHit.(int); okInt {
		activationOnHit = float64(activationOnHitInt)
	}
	s.ActivationOnHit = activationOnHit.(float64)

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
	for _, obj := range highlyContendedHosts {
		state.Vote(obj.ComputeHost, s.ActivationOnHit)
	}
	return nil
}
