// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package vmware

import (
	"github.com/cobaltcore-dev/cortex/internal/features/plugins/vmware"
	"github.com/cobaltcore-dev/cortex/internal/scheduler/plugins"
)

type VROpsAntiAffinityNoisyProjectsStep struct {
	*plugins.StepMixin[struct {
		AvgCPUThreshold float64 `yaml:"avgCPUThreshold"`
		ActivationOnHit float64 `yaml:"activationOnHit"`
	}]
}

func (s *VROpsAntiAffinityNoisyProjectsStep) GetName() string {
	return "vrops_anti_affinity_noisy_projects"
}

// Downvote the hosts a project is currently running on if it's noisy.
func (s *VROpsAntiAffinityNoisyProjectsStep) Run(scenario plugins.Scenario) (map[string]float64, error) {
	activations := s.GetBaseActivations(scenario)
	projectID := scenario.GetProjectID()

	if !scenario.GetVMware() {
		// Only run this step for VMware VMs.
		return activations, nil
	}

	// If the average CPU usage is above the threshold, the project is considered noisy.
	var noisyProjects []vmware.VROpsProjectNoisiness
	if err := s.DB.Get().Model(&noisyProjects).
		Where("avg_cpu_of_project > ?", s.Options.AvgCPUThreshold).
		Where("project = ?", projectID).
		Select(); err != nil {
		return nil, err
	}

	// Get the hosts we need to push the VM away from.
	var hostsByProject = make(map[string][]string)
	for _, p := range noisyProjects {
		hostsByProject[p.Project] = append(hostsByProject[p.Project], p.ComputeHost)
	}
	hostnames, ok := hostsByProject[projectID]
	if !ok {
		// No noisy project, nothing to do.
		return activations, nil
	}
	// Downvote the hosts this project is currently running on.
	for _, host := range hostnames {
		// Only modify the weight if the host is in the scenario.
		if _, ok := activations[host]; ok {
			activations[host] = s.Options.ActivationOnHit
		}
	}
	return activations, nil
}
