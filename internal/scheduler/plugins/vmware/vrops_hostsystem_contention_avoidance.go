// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package vmware

import (
	"github.com/cobaltcore-dev/cortex/internal/features/plugins/vmware"
	"github.com/cobaltcore-dev/cortex/internal/scheduler/plugins"
)

type vROpsAvoidContendedHostsStepOpts struct {
	AvgCPUContentionThreshold float64 `yaml:"avgCPUContentionThreshold"`
	MaxCPUContentionThreshold float64 `yaml:"maxCPUContentionThreshold"`
	ActivationOnHit           float64 `yaml:"activationOnHit"`
}

type VROpsAvoidContendedHostsStep struct {
	plugins.BaseStep[vROpsAvoidContendedHostsStepOpts]
}

func (s *VROpsAvoidContendedHostsStep) GetName() string { return "vrops_avoid_contended_hosts" }

// Downvote hosts that are highly contended.
func (s *VROpsAvoidContendedHostsStep) Run(scenario plugins.Scenario) (map[string]float64, error) {
	activations := s.GetNoEffectActivations(scenario)
	if !scenario.GetVMware() {
		// Only run this step for VMware VMs.
		return activations, nil
	}

	var highlyContendedHosts []vmware.VROpsHostsystemContention
	if err := s.DB.Get().
		Model(&highlyContendedHosts).
		Where("avg_cpu_contention > ?", s.Options.AvgCPUContentionThreshold).
		WhereOr("max_cpu_contention > ?", s.Options.MaxCPUContentionThreshold).
		Select(); err != nil {
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
