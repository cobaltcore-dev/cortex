// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package vmware

import (
	"github.com/cobaltcore-dev/cortex/internal/features/plugins/vmware"
	"github.com/cobaltcore-dev/cortex/internal/scheduler/plugins"
)

// Options for the scheduling step, given through the
// step config in the service yaml file.
type AvoidContendedHostsStepOpts struct {
	AvgCPUContentionThreshold float64 `yaml:"avgCPUContentionThreshold"`
	MaxCPUContentionThreshold float64 `yaml:"maxCPUContentionThreshold"`
	ActivationOnHit           float64 `yaml:"activationOnHit"`
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
func (s *AvoidContendedHostsStep) Run(scenario plugins.Scenario) (map[string]float64, error) {
	activations := s.BaseStep.BaseActivations(scenario)
	if !scenario.GetVMware() {
		// Only run this step for VMware VMs.
		return activations, nil
	}

	var highlyContendedHosts []vmware.VROpsHostsystemContention
	if _, err := s.DB.Select(&highlyContendedHosts, `
		SELECT * FROM feature_vrops_hostsystem_contention
		WHERE avg_cpu_contention > :avg_cpu_contention_threshold
		OR max_cpu_contention > :max_cpu_contention_threshold
	`, map[string]any{
		"avg_cpu_contention_threshold": s.Options.AvgCPUContentionThreshold,
		"max_cpu_contention_threshold": s.Options.MaxCPUContentionThreshold,
	}); err != nil {
		return nil, err
	}

	// Push the VM away from highly contended hosts.
	for _, host := range highlyContendedHosts {
		// Only modify the weight if the host is in the scenario.
		if _, ok := activations[host.ComputeHost]; ok {
			activations[host.ComputeHost] = s.Options.ActivationOnHit
		}
	}
	return activations, nil
}
