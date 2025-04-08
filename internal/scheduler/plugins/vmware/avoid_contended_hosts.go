// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package vmware

import (
	"errors"

	"github.com/cobaltcore-dev/cortex/internal/features/plugins/vmware"
	"github.com/cobaltcore-dev/cortex/internal/scheduler/api"
	"github.com/cobaltcore-dev/cortex/internal/scheduler/plugins"
)

// Options for the scheduling step, given through the
// step config in the service yaml file.
type AvoidContendedHostsStepOpts struct {
	AvgCPUContentionLowerBound float64 `yaml:"avgCPUContentionLowerBound"` // -> mapped to ActivationLowerBound
	AvgCPUContentionUpperBound float64 `yaml:"avgCPUContentionUpperBound"` // -> mapped to ActivationUpperBound

	AvgCPUContentionActivationLowerBound float64 `yaml:"avgCPUContentionActivationLowerBound"`
	AvgCPUContentionActivationUpperBound float64 `yaml:"avgCPUContentionActivationUpperBound"`

	MaxCPUContentionLowerBound float64 `yaml:"maxCPUContentionLowerBound"` // -> mapped to ActivationLowerBound
	MaxCPUContentionUpperBound float64 `yaml:"maxCPUContentionUpperBound"` // -> mapped to ActivationUpperBound

	MaxCPUContentionActivationLowerBound float64 `yaml:"maxCPUContentionActivationLowerBound"`
	MaxCPUContentionActivationUpperBound float64 `yaml:"maxCPUContentionActivationUpperBound"`
}

func (o AvoidContendedHostsStepOpts) Validate() error {
	// Avoid zero-division during min-max scaling.
	if o.AvgCPUContentionLowerBound == o.AvgCPUContentionUpperBound {
		return errors.New("avgCPUContentionLowerBound and avgCPUContentionUpperBound must not be equal")
	}
	if o.MaxCPUContentionLowerBound == o.MaxCPUContentionUpperBound {
		return errors.New("maxCPUContentionLowerBound and maxCPUContentionUpperBound must not be equal")
	}
	return nil
}

// Step to avoid contended hosts by downvoting them.
type AvoidContendedHostsStep struct {
	// BaseStep is a helper struct that provides common functionality for all steps.
	plugins.BaseStep[AvoidContendedHostsStepOpts]
}

// Get the name of this step, used for identification in config, logs, metrics, etc.
func (s *AvoidContendedHostsStep) GetName() string {
	return "vmware_avoid_contended_hosts"
}

// Downvote hosts that are highly contended.
func (s *AvoidContendedHostsStep) Run(request api.Request) (map[string]float64, error) {
	activations := s.BaseActivations(request)
	if !request.GetVMware() {
		// Only run this step for VMware VMs.
		return activations, nil
	}

	var highlyContendedHosts []vmware.VROpsHostsystemContention
	if _, err := s.DB.Select(&highlyContendedHosts, `
		SELECT * FROM feature_vrops_hostsystem_contention
	`); err != nil {
		return nil, err
	}

	// Push the VM away from highly contended hosts.
	for _, host := range highlyContendedHosts {
		// Only modify the weight if the host is in the scenario.
		if _, ok := activations[host.ComputeHost]; !ok {
			continue
		}
		activationAvg := plugins.MinMaxScale(
			host.AvgCPUContention,
			s.Options.AvgCPUContentionLowerBound,
			s.Options.AvgCPUContentionUpperBound,
			s.Options.AvgCPUContentionActivationLowerBound,
			s.Options.AvgCPUContentionActivationUpperBound,
		)
		activationMax := plugins.MinMaxScale(
			host.MaxCPUContention,
			s.Options.MaxCPUContentionLowerBound,
			s.Options.MaxCPUContentionUpperBound,
			s.Options.MaxCPUContentionActivationLowerBound,
			s.Options.MaxCPUContentionActivationUpperBound,
		)
		activations[host.ComputeHost] = activationAvg + activationMax
	}
	return activations, nil
}
