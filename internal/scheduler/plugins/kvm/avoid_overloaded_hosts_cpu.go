// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package kvm

import (
	"errors"

	"github.com/cobaltcore-dev/cortex/internal/features/plugins/kvm"
	"github.com/cobaltcore-dev/cortex/internal/scheduler/api"
	"github.com/cobaltcore-dev/cortex/internal/scheduler/plugins"
)

// Options for the scheduling step, given through the step config in the service yaml file.
// Use the options contained in this struct to configure the bounds for min-max scaling.
type AvoidOverloadedHostsCPUStepOpts struct {
	AvgCPUUsageLowerBound float64 `yaml:"avgCPUUsageLowerBound"` // -> mapped to ActivationLowerBound
	AvgCPUUsageUpperBound float64 `yaml:"avgCPUUsageUpperBound"` // -> mapped to ActivationUpperBound

	AvgCPUUsageActivationLowerBound float64 `yaml:"avgCPUUsageActivationLowerBound"`
	AvgCPUUsageActivationUpperBound float64 `yaml:"avgCPUUsageActivationUpperBound"`

	MaxCPUUsageLowerBound float64 `yaml:"maxCPUUsageLowerBound"` // -> mapped to ActivationLowerBound
	MaxCPUUsageUpperBound float64 `yaml:"maxCPUUsageUpperBound"` // -> mapped to ActivationUpperBound

	MaxCPUUsageActivationLowerBound float64 `yaml:"maxCPUUsageActivationLowerBound"`
	MaxCPUUsageActivationUpperBound float64 `yaml:"maxCPUUsageActivationUpperBound"`
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
func (s *AvoidOverloadedHostsCPUStep) Run(request api.Request) (map[string]float64, error) {
	activations := s.BaseActivations(request)
	if request.GetVMware() {
		// Don't run this step for VMware VMs.
		return activations, nil
	}

	var hostCPUUsages []kvm.NodeExporterHostCPUUsage
	if _, err := s.DB.Select(&hostCPUUsages, `
		SELECT * FROM feature_host_cpu_usage
	`); err != nil {
		return nil, err
	}

	for _, host := range hostCPUUsages {
		// Only modify the weight if the host is in the scenario.
		if _, ok := activations[host.ComputeHost]; !ok {
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
		activations[host.ComputeHost] = activationAvg + activationMax
	}
	return activations, nil
}
