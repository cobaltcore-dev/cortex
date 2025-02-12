// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package vmware

import (
	"github.com/cobaltcore-dev/cortex/internal/features/plugins/vmware"
	"github.com/cobaltcore-dev/cortex/internal/scheduler/plugins"
)

// Options for the scheduling step, given through the
// step config in the service yaml file.
type vROpsAntiAffinityNoisyProjectsStepOpts struct {
	AvgCPUThreshold float64 `yaml:"avgCPUThreshold"`
	ActivationOnHit float64 `yaml:"activationOnHit"`
}

// Step to avoid noisy projects by downvoting the hosts they are running on.
type VROpsAntiAffinityNoisyProjectsStep struct {
	// BaseStep is a helper struct that provides common functionality for all steps.
	plugins.BaseStep[vROpsAntiAffinityNoisyProjectsStepOpts]
}

// Get the name of this step, used for identification in config, logs, metrics, etc.
func (s *VROpsAntiAffinityNoisyProjectsStep) GetName() string {
	return "vrops_anti_affinity_noisy_projects"
}

// Downvote the hosts a project is currently running on if it's noisy.
func (s *VROpsAntiAffinityNoisyProjectsStep) Run(scenario plugins.Scenario) (map[string]float64, error) {
	activations := s.BaseStep.BaseActivations(scenario)
	if !scenario.GetVMware() {
		// Only run this step for VMware VMs.
		return activations, nil
	}

	projectID := scenario.GetProjectID()

	// If the average CPU usage is above the threshold, the project is considered noisy.
	var noisyProjects []vmware.VROpsProjectNoisiness
	if _, err := s.DB.Select(&noisyProjects, `
		SELECT * FROM feature_vrops_project_noisiness
		WHERE avg_cpu_of_project > :avg_cpu_threshold
		AND project = :project_id
	`, map[string]any{
		"avg_cpu_threshold": s.Options.AvgCPUThreshold,
		"project_id":        projectID,
	}); err != nil {
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
