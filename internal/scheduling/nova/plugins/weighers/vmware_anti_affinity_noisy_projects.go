// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package weighers

import (
	"context"
	"errors"
	"log/slog"

	api "github.com/cobaltcore-dev/cortex/api/external/nova"
	"github.com/cobaltcore-dev/cortex/api/v1alpha1"
	"github.com/cobaltcore-dev/cortex/internal/knowledge/extractor/plugins/compute"
	"github.com/cobaltcore-dev/cortex/internal/scheduling/lib"
	corev1 "k8s.io/api/core/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// Options for the scheduling step, given through the step config in the service yaml file.
// Use the options contained in this struct to configure the bounds for min-max scaling.
type VMwareAntiAffinityNoisyProjectsStepOpts struct {
	AvgCPUUsageLowerBound float64 `json:"avgCPUUsageLowerBound"` // -> mapped to ActivationLowerBound
	AvgCPUUsageUpperBound float64 `json:"avgCPUUsageUpperBound"` // -> mapped to ActivationUpperBound

	AvgCPUUsageActivationLowerBound float64 `json:"avgCPUUsageActivationLowerBound"`
	AvgCPUUsageActivationUpperBound float64 `json:"avgCPUUsageActivationUpperBound"`
}

func (o VMwareAntiAffinityNoisyProjectsStepOpts) Validate() error {
	// Avoid zero-division during min-max scaling.
	if o.AvgCPUUsageLowerBound == o.AvgCPUUsageUpperBound {
		return errors.New("avgCPUUsageLowerBound and avgCPUUsageUpperBound must not be equal")
	}
	return nil
}

// Step to avoid noisy projects by downvoting the hosts they are running on.
type VMwareAntiAffinityNoisyProjectsStep struct {
	// BaseStep is a helper struct that provides common functionality for all steps.
	lib.BaseWeigher[api.ExternalSchedulerRequest, VMwareAntiAffinityNoisyProjectsStepOpts]
}

// Initialize the step and validate that all required knowledges are ready.
func (s *VMwareAntiAffinityNoisyProjectsStep) Init(ctx context.Context, client client.Client, weigher v1alpha1.WeigherSpec) error {
	if err := s.BaseWeigher.Init(ctx, client, weigher); err != nil {
		return err
	}
	if err := s.CheckKnowledges(ctx, corev1.ObjectReference{Name: "vmware-project-noisiness"}); err != nil {
		return err
	}
	return nil
}

// Downvote the hosts a project is currently running on if it's noisy.
func (s *VMwareAntiAffinityNoisyProjectsStep) Run(traceLog *slog.Logger, request api.ExternalSchedulerRequest) (*lib.FilterWeigherPipelineStepResult, error) {
	result := s.IncludeAllHostsFromRequest(request)

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
		UnboxFeatureList[compute.VROpsProjectNoisiness](knowledge.Status.Raw)
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
		result.Statistics["avg cpu usage of this project"].Hosts[p.ComputeHost] = p.AvgCPUOfProject
	}
	return result, nil
}

func init() {
	Index["vmware_anti_affinity_noisy_projects"] = func() lib.Weigher[api.ExternalSchedulerRequest] {
		return &VMwareAntiAffinityNoisyProjectsStep{}
	}
}
