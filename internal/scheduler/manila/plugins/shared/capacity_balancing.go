// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package shared

import (
	"errors"
	"log/slog"

	"github.com/cobaltcore-dev/cortex/internal/extractor/plugins/shared"
	"github.com/cobaltcore-dev/cortex/internal/scheduler"
	"github.com/cobaltcore-dev/cortex/internal/scheduler/manila/api"
)

// Options for the scheduling step, given through the step config in the service yaml file.
type CapacityBalancingStepOpts struct {
	UtilizedLowerBoundPct        float64 `json:"utilizedLowerBoundPct"` // -> mapped to ActivationLowerBound
	UtilizedUpperBoundPct        float64 `json:"utilizedUpperBoundPct"` // -> mapped to ActivationUpperBound
	UtilizedActivationLowerBound float64 `json:"utilizedActivationLowerBound"`
	UtilizedActivationUpperBound float64 `json:"utilizedActivationUpperBound"`
}

func (o CapacityBalancingStepOpts) Validate() error {
	// Avoid zero-division during min-max scaling.
	if o.UtilizedLowerBoundPct == o.UtilizedUpperBoundPct {
		return errors.New("utilizedLowerBoundPct and utilizedUpperBoundPct must not be equal")
	}
	return nil
}

// Step to balance shares based on the storage pool's available capacity.
// This step can be used to load-balance, or to bin-pack.
type CapacityBalancingStep struct {
	// BaseStep is a helper struct that provides common functionality for all steps.
	scheduler.BaseStep[api.ExternalSchedulerRequest, CapacityBalancingStepOpts]
}

// Get the name of this step, used for identification in config, logs, metrics, etc.
func (s *CapacityBalancingStep) GetName() string {
	return "capacity_balancing"
}

// Pack VMs on hosts based on their flavor.
func (s *CapacityBalancingStep) Run(traceLog *slog.Logger, request api.ExternalSchedulerRequest) (*scheduler.StepResult, error) {
	result := s.PrepareResult(request)
	result.Statistics["capacity utilized"] = s.PrepareStats(request, "%")

	var storagePoolUtilizations []shared.StoragePoolUtilization
	if _, err := s.DB.Select(
		&storagePoolUtilizations, "SELECT * FROM "+shared.StoragePoolUtilization{}.TableName(),
	); err != nil {
		return nil, err
	}
	for _, storagePoolUtilization := range storagePoolUtilizations {
		// Only modify the weight if the storage pool is in the request.
		if _, ok := result.Activations[storagePoolUtilization.StoragePoolName]; !ok {
			continue
		}
		activation := scheduler.MinMaxScale(
			storagePoolUtilization.CapacityUtilizedPct,
			s.Options.UtilizedLowerBoundPct,
			s.Options.UtilizedUpperBoundPct,
			s.Options.UtilizedActivationLowerBound,
			s.Options.UtilizedActivationUpperBound,
		)
		result.
			Statistics["capacity utilized"].
			Subjects[storagePoolUtilization.StoragePoolName] = storagePoolUtilization.CapacityUtilizedPct
		result.Activations[storagePoolUtilization.StoragePoolName] = activation
	}
	return result, nil
}
