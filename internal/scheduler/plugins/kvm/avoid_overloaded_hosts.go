// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package kvm

import (
	"github.com/cobaltcore-dev/cortex/internal/features/plugins/kvm"
	"github.com/cobaltcore-dev/cortex/internal/scheduler/plugins"
)

// Options for the scheduling step, given through the
// step config in the service yaml file.
type AvoidOverloadedHostsStepOpts struct {
	AvgCPUUsageThreshold float64 `yaml:"avgCPUUsageThreshold"`
	MaxCPUUsageThreshold float64 `yaml:"maxCPUUsageThreshold"`
	ActivationOnHit      float64 `yaml:"activationOnHit"`
}

// Step to avoid high cpu hosts by downvoting them.
type AvoidOverloadedHostsStep struct {
	// BaseStep is a helper struct that provides common functionality for all steps.
	plugins.BaseStep[AvoidOverloadedHostsStepOpts]
}

// Get the name of this step, used for identification in config, logs, metrics, etc.
func (s *AvoidOverloadedHostsStep) GetName() string {
	return "kvm_avoid_overloaded_hosts"
}

// Downvote hosts that have high cpu load.
func (s *AvoidOverloadedHostsStep) Run(scenario plugins.Scenario) (map[string]float64, error) {
	activations := s.BaseStep.BaseActivations(scenario)
	if scenario.GetVMware() {
		// Don't run this step for VMware VMs.
		return activations, nil
	}

	var highlyUsedHosts []kvm.NodeExporterHostCPUUsage
	if _, err := s.DB.Select(&highlyUsedHosts, `
		SELECT * FROM feature_host_cpu_usage
		WHERE avg_cpu_usage > :avg_cpu_usage_threshold
		OR max_cpu_usage > :max_cpu_usage_threshold
	`, map[string]any{
		"avg_cpu_usage_threshold": s.Options.AvgCPUUsageThreshold,
		"max_cpu_usage_threshold": s.Options.MaxCPUUsageThreshold,
	}); err != nil {
		return nil, err
	}

	// Push the VM away from highly used hosts.
	for _, host := range highlyUsedHosts {
		// Only modify the weight if the host is in the scenario.
		if _, ok := activations[host.ComputeHost]; ok {
			activations[host.ComputeHost] = s.Options.ActivationOnHit
		}
	}
	return activations, nil
}
