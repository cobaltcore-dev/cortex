// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package vmware

import (
	"errors"

	"github.com/cobaltcore-dev/cortex/internal/features/plugins/vmware"
	"github.com/cobaltcore-dev/cortex/internal/scheduler/api"
	"github.com/cobaltcore-dev/cortex/internal/scheduler/plugins"
)

// Options for the scheduling step, given through the step config in the service yaml file.
// Use the options contained in this struct to configure the bounds for min-max scaling.
type AntiAffinityNoisyProjectsStepOpts struct {
	AvgCPUUsageLowerBound float64 `yaml:"avgCPUUsageLowerBound"` // -> mapped to ActivationLowerBound
	AvgCPUUsageUpperBound float64 `yaml:"avgCPUUsageUpperBound"` // -> mapped to ActivationUpperBound

	AvgCPUUsageActivationLowerBound float64 `yaml:"avgCPUUsageActivationLowerBound"`
	AvgCPUUsageActivationUpperBound float64 `yaml:"avgCPUUsageActivationUpperBound"`
}

func (o AntiAffinityNoisyProjectsStepOpts) Validate() error {
	// Avoid zero-division during min-max scaling.
	if o.AvgCPUUsageLowerBound == o.AvgCPUUsageUpperBound {
		return errors.New("avgCPUUsageLowerBound and avgCPUUsageUpperBound must not be equal")
	}
	return nil
}

// Step to avoid noisy projects by downvoting the hosts they are running on.
type AntiAffinityNoisyProjectsStep struct {
	// BaseStep is a helper struct that provides common functionality for all steps.
	plugins.BaseStep[AntiAffinityNoisyProjectsStepOpts]
}

// Get the name of this step, used for identification in config, logs, metrics, etc.
func (s *AntiAffinityNoisyProjectsStep) GetName() string {
	return "vmware_anti_affinity_noisy_projects"
}

// Downvote the hosts a project is currently running on if it's noisy.
func (s *AntiAffinityNoisyProjectsStep) Run(request api.Request) (map[string]float64, error) {
	activations := s.BaseActivations(request)
	if !request.VMware {
		// Only run this step for VMware VMs.
		return activations, nil
	}

	// Check how noisy the project is on the compute hosts.
	var projectNoisinessOnHosts []vmware.VROpsProjectNoisiness
	if _, err := s.DB.Select(&projectNoisinessOnHosts, `
		SELECT * FROM feature_vrops_project_noisiness
		WHERE project = :project_id
	`, map[string]any{
		"project_id": request.Spec.Data.ProjectID,
	}); err != nil {
		return nil, err
	}

	for _, p := range projectNoisinessOnHosts {
		// Only modify the weight if the host is in the scenario.
		if _, ok := activations[p.ComputeHost]; !ok {
			continue
		}
		activations[p.ComputeHost] = plugins.MinMaxScale(
			p.AvgCPUOfProject,
			s.Options.AvgCPUUsageLowerBound,
			s.Options.AvgCPUUsageUpperBound,
			s.Options.AvgCPUUsageActivationLowerBound,
			s.Options.AvgCPUUsageActivationUpperBound,
		)
	}
	return activations, nil
}
