// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package kvm

import (
	"fmt"
	"log/slog"

	"github.com/cobaltcore-dev/cortex/knowledge/api/features/kvm"
	"github.com/cobaltcore-dev/cortex/scheduling/internal/descheduling/nova/plugins"
)

type AvoidHighStealPctStepOpts struct {
	// Max steal pct threshold above which VMs should be descheduled.
	MaxStealPctOverObservedTimeSpan float64 `json:"maxStealPctOverObservedTimeSpan"`
}

type AvoidHighStealPctStep struct {
	// BaseStep is a helper struct that provides common functionality for all steps.
	plugins.BaseStep[AvoidHighStealPctStepOpts]
}

// Get the name of this step, used for identification in config, logs, metrics, etc.
func (s *AvoidHighStealPctStep) GetName() string {
	return "avoid_high_steal_pct"
}

func (s *AvoidHighStealPctStep) Run() ([]plugins.Decision, error) {
	if s.Options.MaxStealPctOverObservedTimeSpan <= 0 {
		slog.Info("skipping step because maxStealPctOverObservedTimeSpan is not set or <= 0")
		return nil, nil
	}
	// Get VMs matching the MaxStealPctOverObservedTimeSpan option.
	var decisions []plugins.Decision
	var features []kvm.LibvirtDomainCPUStealPct
	table := kvm.LibvirtDomainCPUStealPct{}.TableName()
	if _, err := s.DB.Select(&features, "SELECT * FROM "+table); err != nil {
		return nil, err
	}
	for _, f := range features {
		if f.MaxStealTimePct > s.Options.MaxStealPctOverObservedTimeSpan {
			decisions = append(decisions, plugins.Decision{
				VMID:   f.InstanceUUID,
				Reason: fmt.Sprintf("kvm monitoring indicates cpu steal pct %.2f%% which is above %.2f%% threshold", f.MaxStealTimePct, s.Options.MaxStealPctOverObservedTimeSpan),
				Host:   f.Host,
			})
			slog.Info("vm marked for descheduling due to high cpu steal pct",
				"instanceUUID", f.InstanceUUID,
				"maxStealTimePct", f.MaxStealTimePct,
			)
		}
	}
	return decisions, nil
}
