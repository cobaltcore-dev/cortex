// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package weighers

import (
	"context"
	"errors"
	"log/slog"

	api "github.com/cobaltcore-dev/cortex/api/external/manila"
	"github.com/cobaltcore-dev/cortex/api/v1alpha1"
	"github.com/cobaltcore-dev/cortex/internal/knowledge/extractor/plugins/storage"
	"github.com/cobaltcore-dev/cortex/internal/scheduling/lib"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// Options for the scheduling step, given through the step config in the service
// yaml file.
type NetappCPUUsageBalancingStepOpts struct {
	AvgCPUUsageLowerBound float64 `json:"avgCPUUsageLowerBound"` // -> mapped to ActivationLowerBound
	AvgCPUUsageUpperBound float64 `json:"avgCPUUsageUpperBound"` // -> mapped to ActivationUpperBound

	AvgCPUUsageActivationLowerBound float64 `json:"avgCPUUsageActivationLowerBound"`
	AvgCPUUsageActivationUpperBound float64 `json:"avgCPUUsageActivationUpperBound"`

	MaxCPUUsageLowerBound float64 `json:"maxCPUUsageLowerBound"` // -> mapped to ActivationLowerBound
	MaxCPUUsageUpperBound float64 `json:"maxCPUUsageUpperBound"` // -> mapped to ActivationUpperBound

	MaxCPUUsageActivationLowerBound float64 `json:"maxCPUUsageActivationLowerBound"`
	MaxCPUUsageActivationUpperBound float64 `json:"maxCPUUsageActivationUpperBound"`
}

func (o NetappCPUUsageBalancingStepOpts) Validate() error {
	// Avoid zero-division during min-max scaling.
	if o.AvgCPUUsageLowerBound == o.AvgCPUUsageUpperBound {
		return errors.New("avgCPUUsageLowerBound and avgCPUUsageUpperBound must not be equal")
	}
	if o.MaxCPUUsageLowerBound == o.MaxCPUUsageUpperBound {
		return errors.New("maxCPUUsageLowerBound and maxCPUUsageUpperBound must not be equal")
	}
	return nil
}

// Step to balance CPU usage by avoiding highly used storage pools.
type NetappCPUUsageBalancingStep struct {
	// BaseStep is a helper struct that provides common functionality for all steps.
	lib.BaseWeigher[api.ExternalSchedulerRequest, NetappCPUUsageBalancingStepOpts]
}

// Initialize the step and validate that all required knowledges are ready.
func (s *NetappCPUUsageBalancingStep) Init(ctx context.Context, client client.Client, weigher v1alpha1.WeigherSpec) error {
	if err := s.BaseWeigher.Init(ctx, client, weigher); err != nil {
		return err
	}
	if err := s.CheckKnowledges(ctx, types.NamespacedName{Name: "netapp-storage-pool-cpu-usage-manila"}); err != nil {
		return err
	}
	return nil
}

// Downvote hosts that are highly contended.
func (s *NetappCPUUsageBalancingStep) Run(ctx context.Context, traceLog *slog.Logger, request api.ExternalSchedulerRequest) (*lib.FilterWeigherPipelineStepResult, error) {
	result := s.IncludeAllHostsFromRequest(request)
	result.Statistics["avg cpu contention"] = s.PrepareStats(request, "%")
	result.Statistics["max cpu contention"] = s.PrepareStats(request, "%")

	knowledge := &v1alpha1.Knowledge{}
	if err := s.Client.Get(
		ctx,
		client.ObjectKey{Name: "netapp-storage-pool-cpu-usage-manila"},
		knowledge,
	); err != nil {
		return nil, err
	}
	usages, err := v1alpha1.
		UnboxFeatureList[storage.StoragePoolCPUUsage](knowledge.Status.Raw)
	if err != nil {
		return nil, err
	}

	// Push the share away from highly used storage pools.
	for _, usage := range usages {
		// Only modify the weight if the host is in the scenario.
		if _, ok := result.Activations[usage.StoragePoolName]; !ok {
			continue
		}
		activationAvg := lib.MinMaxScale(
			usage.AvgCPUUsagePct,
			s.Options.AvgCPUUsageLowerBound,
			s.Options.AvgCPUUsageUpperBound,
			s.Options.AvgCPUUsageActivationLowerBound,
			s.Options.AvgCPUUsageActivationUpperBound,
		)
		activationMax := lib.MinMaxScale(
			usage.MaxCPUUsagePct,
			s.Options.MaxCPUUsageLowerBound,
			s.Options.MaxCPUUsageUpperBound,
			s.Options.MaxCPUUsageActivationLowerBound,
			s.Options.MaxCPUUsageActivationUpperBound,
		)
		result.Activations[usage.StoragePoolName] = activationAvg + activationMax
		result.Statistics["avg cpu contention"].Hosts[usage.StoragePoolName] = usage.AvgCPUUsagePct
		result.Statistics["max cpu contention"].Hosts[usage.StoragePoolName] = usage.MaxCPUUsagePct
	}
	return result, nil
}

func init() {
	Index["netapp_cpu_usage_balancing"] = func() lib.Weigher[api.ExternalSchedulerRequest] {
		return &NetappCPUUsageBalancingStep{}
	}
}
