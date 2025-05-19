// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package kvm

import (
	"errors"
	"log/slog"

	"github.com/cobaltcore-dev/cortex/internal/features/plugins/kvm"
	"github.com/cobaltcore-dev/cortex/internal/scheduler/api"
	"github.com/cobaltcore-dev/cortex/internal/scheduler/plugins"
)

// Options for the scheduling step, given through the step config in the service yaml file.
// Use the options contained in this struct to configure the bounds for min-max scaling.
type AvoidOverloadedHostsCPUStepOpts struct {
	AvgCPUUsageLowerBound float64 `json:"avgCPUUsageLowerBound"` // -> mapped to ActivationLowerBound
	AvgCPUUsageUpperBound float64 `json:"avgCPUUsageUpperBound"` // -> mapped to ActivationUpperBound

	AvgCPUUsageActivationLowerBound float64 `json:"avgCPUUsageActivationLowerBound"`
	AvgCPUUsageActivationUpperBound float64 `json:"avgCPUUsageActivationUpperBound"`

	MaxCPUUsageLowerBound float64 `json:"maxCPUUsageLowerBound"` // -> mapped to ActivationLowerBound
	MaxCPUUsageUpperBound float64 `json:"maxCPUUsageUpperBound"` // -> mapped to ActivationUpperBound

	MaxCPUUsageActivationLowerBound float64 `json:"maxCPUUsageActivationLowerBound"`
	MaxCPUUsageActivationUpperBound float64 `json:"maxCPUUsageActivationUpperBound"`
}

func (o AvoidOverloadedHostsCPUStepOpts) Validate() error {
	// Avoid zero-division during min-max scaling.
	if o.AvgCPUUsageLowerBound == o.AvgCPUUsageUpperBound {
		return errors.New("avgCPUUsageLowerBound and avgCPUUsageUpperBound must not be equal")
	}
	if o.MaxCPUUsageLowerBound == o.MaxCPUUsageUpperBound {
		return errors.New("maxCPUUsageLowerBound and maxCPUUsageUpperBound must not be equal")
	}
	return nil
}

// Step to avoid high cpu hosts by downvoting them.
type AvoidOverloadedHostsCPUStep struct {
	// BaseStep is a helper struct that provides common functionality for all steps.
	plugins.BaseStep[AvoidOverloadedHostsCPUStepOpts]
}

// Get the name of this step, used for identification in config, logs, metrics, etc.
func (s *AvoidOverloadedHostsCPUStep) GetName() string {
	return "kvm_avoid_overloaded_hosts_cpu"
}

// Downvote hosts that have high cpu load.
func (s *AvoidOverloadedHostsCPUStep) Run(traceLog *slog.Logger, request api.Request) (*plugins.StepResult, error) {
	result := s.BaseResult(request)
	if request.GetVMware() {
		// Don't run this step for VMware VMs.
		return result, nil
	}

	var hostCPUUsages []kvm.NodeExporterHostCPUUsage
	if _, err := s.DB.Select(&hostCPUUsages, `
		SELECT * FROM feature_host_cpu_usage
	`); err != nil {
		return nil, err
	}

	for _, host := range hostCPUUsages {
		// Only modify the weight if the host is in the scenario.
		if _, ok := result.Activations[host.ComputeHost]; !ok {
			continue
		}
		activationAvg := plugins.MinMaxScale(
			host.AvgCPUUsage,
			s.Options.AvgCPUUsageLowerBound,
			s.Options.AvgCPUUsageUpperBound,
			s.Options.AvgCPUUsageActivationLowerBound,
			s.Options.AvgCPUUsageActivationUpperBound,
		)
		activationMax := plugins.MinMaxScale(
			host.MaxCPUUsage,
			s.Options.MaxCPUUsageLowerBound,
			s.Options.MaxCPUUsageUpperBound,
			s.Options.MaxCPUUsageActivationLowerBound,
			s.Options.MaxCPUUsageActivationUpperBound,
		)
		result.Activations[host.ComputeHost] = activationAvg + activationMax
	}
	return result, nil
}
