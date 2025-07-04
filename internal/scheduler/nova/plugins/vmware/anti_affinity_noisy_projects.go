// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package vmware

import (
	"errors"
	"log/slog"

	"github.com/cobaltcore-dev/cortex/internal/extractor/plugins/vmware"
	"github.com/cobaltcore-dev/cortex/internal/scheduler"
	"github.com/cobaltcore-dev/cortex/internal/scheduler/nova/api"
)

// Options for the scheduling step, given through the step config in the service yaml file.
// Use the options contained in this struct to configure the bounds for min-max scaling.
type AntiAffinityNoisyProjectsStepOpts struct {
	AvgCPUUsageLowerBound float64 `json:"avgCPUUsageLowerBound"` // -> mapped to ActivationLowerBound
	AvgCPUUsageUpperBound float64 `json:"avgCPUUsageUpperBound"` // -> mapped to ActivationUpperBound

	AvgCPUUsageActivationLowerBound float64 `json:"avgCPUUsageActivationLowerBound"`
	AvgCPUUsageActivationUpperBound float64 `json:"avgCPUUsageActivationUpperBound"`
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
	scheduler.BaseStep[api.ExternalSchedulerRequest, AntiAffinityNoisyProjectsStepOpts]
}

// Get the name of this step, used for identification in config, logs, metrics, etc.
func (s *AntiAffinityNoisyProjectsStep) GetName() string {
	return "vmware_anti_affinity_noisy_projects"
}

// Downvote the hosts a project is currently running on if it's noisy.
func (s *AntiAffinityNoisyProjectsStep) Run(traceLog *slog.Logger, request api.ExternalSchedulerRequest) (*scheduler.StepResult, error) {
	result := s.PrepareResult(request)
	result.Statistics["avg cpu usage of this project"] = s.PrepareStats(request, "%")

	// Check how noisy the project is on the compute hosts.
	var projectNoisinessOnHosts []vmware.VROpsProjectNoisiness
	group := "scheduler-nova"
	if _, err := s.DB.SelectTimed(group, &projectNoisinessOnHosts, `
		SELECT * FROM feature_vrops_project_noisiness
		WHERE project = :project_id
	`, map[string]any{
		"project_id": request.Spec.Data.ProjectID,
	}); err != nil {
		return nil, err
	}

	for _, p := range projectNoisinessOnHosts {
		// Only modify the weight if the host is in the scenario.
		if _, ok := result.Activations[p.ComputeHost]; !ok {
			continue
		}
		result.Activations[p.ComputeHost] = scheduler.MinMaxScale(
			p.AvgCPUOfProject,
			s.Options.AvgCPUUsageLowerBound,
			s.Options.AvgCPUUsageUpperBound,
			s.Options.AvgCPUUsageActivationLowerBound,
			s.Options.AvgCPUUsageActivationUpperBound,
		)
		result.Statistics["avg cpu usage of this project"].Subjects[p.ComputeHost] = p.AvgCPUOfProject
	}
	return result, nil
}
