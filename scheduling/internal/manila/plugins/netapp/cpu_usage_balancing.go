// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package netapp

import (
	"errors"
	"log/slog"

	"github.com/cobaltcore-dev/cortex/knowledge/api/features/netapp"
	"github.com/cobaltcore-dev/cortex/lib/scheduling"
	"github.com/cobaltcore-dev/cortex/scheduling/internal/manila/api"
)

// Options for the scheduling step, given through the step config in the service
// yaml file.
type CPUUsageBalancingStepOpts struct {
	AvgCPUUsageLowerBound float64 `json:"avgCPUUsageLowerBound"` // -> mapped to ActivationLowerBound
	AvgCPUUsageUpperBound float64 `json:"avgCPUUsageUpperBound"` // -> mapped to ActivationUpperBound

	AvgCPUUsageActivationLowerBound float64 `json:"avgCPUUsageActivationLowerBound"`
	AvgCPUUsageActivationUpperBound float64 `json:"avgCPUUsageActivationUpperBound"`

	MaxCPUUsageLowerBound float64 `json:"maxCPUUsageLowerBound"` // -> mapped to ActivationLowerBound
	MaxCPUUsageUpperBound float64 `json:"maxCPUUsageUpperBound"` // -> mapped to ActivationUpperBound

	MaxCPUUsageActivationLowerBound float64 `json:"maxCPUUsageActivationLowerBound"`
	MaxCPUUsageActivationUpperBound float64 `json:"maxCPUUsageActivationUpperBound"`
}

func (o CPUUsageBalancingStepOpts) Validate() error {
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
type CPUUsageBalancingStep struct {
	// BaseStep is a helper struct that provides common functionality for all steps.
	scheduling.BaseStep[api.PipelineRequest, CPUUsageBalancingStepOpts]
}

// Get the name of this step, used for identification in config, logs, metrics, etc.
func (s *CPUUsageBalancingStep) GetName() string {
	return "netapp_cpu_usage_balancing"
}

// Downvote hosts that are highly contended.
func (s *CPUUsageBalancingStep) Run(traceLog *slog.Logger, request api.PipelineRequest) (*scheduling.StepResult, error) {
	result := s.PrepareResult(request)
	result.Statistics["avg cpu contention"] = s.PrepareStats(request, "%")
	result.Statistics["max cpu contention"] = s.PrepareStats(request, "%")

	var usages []netapp.StoragePoolCPUUsage
	group := "scheduler-manila"
	if _, err := s.DB.SelectTimed(group, &usages,
		"SELECT * FROM "+netapp.StoragePoolCPUUsage{}.TableName(),
	); err != nil {
		return nil, err
	}

	// Push the share away from highly used storage pools.
	for _, usage := range usages {
		// Only modify the weight if the host is in the scenario.
		if _, ok := result.Activations[usage.StoragePoolName]; !ok {
			continue
		}
		activationAvg := scheduling.MinMaxScale(
			usage.AvgCPUUsagePct,
			s.Options.AvgCPUUsageLowerBound,
			s.Options.AvgCPUUsageUpperBound,
			s.Options.AvgCPUUsageActivationLowerBound,
			s.Options.AvgCPUUsageActivationUpperBound,
		)
		activationMax := scheduling.MinMaxScale(
			usage.MaxCPUUsagePct,
			s.Options.MaxCPUUsageLowerBound,
			s.Options.MaxCPUUsageUpperBound,
			s.Options.MaxCPUUsageActivationLowerBound,
			s.Options.MaxCPUUsageActivationUpperBound,
		)
		result.Activations[usage.StoragePoolName] = activationAvg + activationMax
		result.Statistics["avg cpu contention"].Subjects[usage.StoragePoolName] = usage.AvgCPUUsagePct
		result.Statistics["max cpu contention"].Subjects[usage.StoragePoolName] = usage.MaxCPUUsagePct
	}
	return result, nil
}
