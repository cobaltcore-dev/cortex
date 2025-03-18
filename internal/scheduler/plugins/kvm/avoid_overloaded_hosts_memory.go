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
type AvoidOverloadedHostsMemoryStepOpts struct {
	AvgMemoryUsageLowerBound float64 `yaml:"avgMemoryUsageLowerBound"` // -> mapped to ActivationLowerBound
	AvgMemoryUsageUpperBound float64 `yaml:"avgMemoryUsageUpperBound"` // -> mapped to ActivationUpperBound

	AvgMemoryUsageActivationLowerBound float64 `yaml:"avgMemoryUsageActivationLowerBound"`
	AvgMemoryUsageActivationUpperBound float64 `yaml:"avgMemoryUsageActivationUpperBound"`

	MaxMemoryUsageLowerBound float64 `yaml:"maxMemoryUsageLowerBound"` // -> mapped to ActivationLowerBound
	MaxMemoryUsageUpperBound float64 `yaml:"maxMemoryUsageUpperBound"` // -> mapped to ActivationUpperBound

	MaxMemoryUsageActivationLowerBound float64 `yaml:"maxMemoryUsageActivationLowerBound"`
	MaxMemoryUsageActivationUpperBound float64 `yaml:"maxMemoryUsageActivationUpperBound"`
}

func (o AvoidOverloadedHostsMemoryStepOpts) Validate() error {
	// Avoid zero-division during min-max scaling.
	if o.AvgMemoryUsageLowerBound == o.AvgMemoryUsageUpperBound {
		return errors.New("avgMemoryUsageLowerBound and avgMemoryUsageUpperBound must not be equal")
	}
	if o.MaxMemoryUsageLowerBound == o.MaxMemoryUsageUpperBound {
		return errors.New("maxMemoryUsageLowerBound and maxMemoryUsageUpperBound must not be equal")
	}
	return nil
}

// Step to avoid high cpu hosts by downvoting them.
type AvoidOverloadedHostsMemoryStep struct {
	// BaseStep is a helper struct that provides common functionality for all steps.
	plugins.BaseStep[AvoidOverloadedHostsMemoryStepOpts]
}

// Get the name of this step, used for identification in config, logs, metrics, etc.
func (s *AvoidOverloadedHostsMemoryStep) GetName() string {
	return "kvm_avoid_overloaded_hosts_memory"
}

// Downvote hosts that have high cpu load.
func (s *AvoidOverloadedHostsMemoryStep) Run(request api.Request) (map[string]float64, error) {
	activations := s.BaseStep.BaseActivations(request)
	if request.VMware {
		// Don't run this step for VMware VMs.
		return activations, nil
	}

	var hostMemoryActive []kvm.NodeExporterHostMemoryActive
	if _, err := s.DB.Select(&hostMemoryActive, `
		SELECT * FROM feature_host_memory_active
	`); err != nil {
		return nil, err
	}

	for _, host := range hostMemoryActive {
		// Only modify the weight if the host is in the scenario.
		if _, ok := activations[host.ComputeHost]; !ok {
			continue
		}
		activationAvg := plugins.MinMaxScale(
			host.AvgMemoryActive,
			s.Options.AvgMemoryUsageLowerBound,
			s.Options.AvgMemoryUsageUpperBound,
			s.Options.AvgMemoryUsageActivationLowerBound,
			s.Options.AvgMemoryUsageActivationUpperBound,
		)
		activationMax := plugins.MinMaxScale(
			host.MaxMemoryActive,
			s.Options.MaxMemoryUsageLowerBound,
			s.Options.MaxMemoryUsageUpperBound,
			s.Options.MaxMemoryUsageActivationLowerBound,
			s.Options.MaxMemoryUsageActivationUpperBound,
		)
		activations[host.ComputeHost] = activationAvg + activationMax
	}
	return activations, nil
}
