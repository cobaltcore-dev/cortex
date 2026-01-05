// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package kvm

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/cobaltcore-dev/cortex/api/v1alpha1"
	"github.com/cobaltcore-dev/cortex/internal/knowledge/extractor/plugins/compute"
	"github.com/cobaltcore-dev/cortex/internal/scheduling/descheduling/nova/plugins"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

type AvoidHighStealPctStepOpts struct {
	// Max steal pct threshold above which VMs should be descheduled.
	MaxStealPctOverObservedTimeSpan float64 `json:"maxStealPctOverObservedTimeSpan"`
}

type AvoidHighStealPctStep struct {
	// BaseStep is a helper struct that provides common functionality for all steps.
	plugins.BaseStep[AvoidHighStealPctStepOpts]
}

func (s *AvoidHighStealPctStep) Run() ([]plugins.Decision, error) {
	if s.Options.MaxStealPctOverObservedTimeSpan <= 0 {
		slog.Info("skipping step because maxStealPctOverObservedTimeSpan is not set or <= 0")
		return nil, nil
	}
	// Get VMs matching the MaxStealPctOverObservedTimeSpan option.
	knowledge := &v1alpha1.Knowledge{}
	if err := s.Client.Get(
		context.Background(),
		client.ObjectKey{Name: "kvm-libvirt-domain-cpu-steal-pct"},
		knowledge,
	); err != nil {
		return nil, err
	}
	features, err := v1alpha1.
		UnboxFeatureList[compute.LibvirtDomainCPUStealPct](knowledge.Status.Raw)
	if err != nil {
		return nil, err
	}
	var decisions []plugins.Decision
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
