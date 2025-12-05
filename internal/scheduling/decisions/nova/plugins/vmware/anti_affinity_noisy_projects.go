// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package vmware

import (
	"context"
	"errors"
	"log/slog"

	api "github.com/cobaltcore-dev/cortex/api/delegation/nova"
	"github.com/cobaltcore-dev/cortex/api/v1alpha1"
	"github.com/cobaltcore-dev/cortex/internal/knowledge/extractor/plugins/vmware"
	"github.com/cobaltcore-dev/cortex/internal/scheduling/lib"
	"sigs.k8s.io/controller-runtime/pkg/client"
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
	lib.BaseStep[api.ExternalSchedulerRequest, AntiAffinityNoisyProjectsStepOpts]
}

// Downvote the hosts a project is currently running on if it's noisy.
func (s *AntiAffinityNoisyProjectsStep) Run(traceLog *slog.Logger, request api.ExternalSchedulerRequest) (*lib.StepResult, error) {
	result := s.PrepareResult(request)

	if !request.VMware {
		slog.Debug("Skipping general purpose balancing step for non-VMware VM")
		return result, nil
	}

	result.Statistics["avg cpu usage of this project"] = s.PrepareStats(request, "%")

	knowledge := &v1alpha1.Knowledge{}
	if err := s.Client.Get(
		context.Background(),
		client.ObjectKey{Name: "vmware-project-noisiness"},
		knowledge,
	); err != nil {
		return nil, err
	}
	projectNoisinessOnHosts, err := v1alpha1.
		UnboxFeatureList[vmware.VROpsProjectNoisiness](knowledge.Status.Raw)
	if err != nil {
		return nil, err
	}
	for _, p := range projectNoisinessOnHosts {
		if p.Project != request.Spec.Data.ProjectID {
			continue
		}
		// Only modify the weight if the host is in the scenario.
		if _, ok := result.Activations[p.ComputeHost]; !ok {
			continue
		}
		result.Activations[p.ComputeHost] = lib.MinMaxScale(
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
