// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package shared

import (
	"errors"
	"log/slog"

	"github.com/cobaltcore-dev/cortex/internal/extractor/plugins/shared"
	"github.com/cobaltcore-dev/cortex/internal/scheduler"
	"github.com/cobaltcore-dev/cortex/internal/scheduler/nova/api"
)

// Options for the scheduling step, given through the step config in the service yaml file.
type ResourceBalancingStepOpts struct {
	CPUEnabled                      bool    `json:"cpuEnabled"`
	CPUUtilizedLowerBoundPct        float64 `json:"cpuUtilizedLowerBoundPct"` // -> mapped to ActivationLowerBound
	CPUUtilizedUpperBoundPct        float64 `json:"cpuUtilizedUpperBoundPct"` // -> mapped to ActivationUpperBound
	CPUUtilizedActivationLowerBound float64 `json:"cpuUtilizedActivationLowerBound"`
	CPUUtilizedActivationUpperBound float64 `json:"cpuUtilizedActivationUpperBound"`

	RAMEnabled                      bool    `json:"ramEnabled"`
	RAMUtilizedLowerBoundPct        float64 `json:"ramUtilizedLowerBoundPct"` // -> mapped to ActivationLowerBound
	RAMUtilizedUpperBoundPct        float64 `json:"ramUtilizedUpperBoundPct"` // -> mapped to ActivationUpperBound
	RAMUtilizedActivationLowerBound float64 `json:"ramUtilizedActivationLowerBound"`
	RAMUtilizedActivationUpperBound float64 `json:"ramUtilizedActivationUpperBound"`

	DiskEnabled                      bool    `json:"diskEnabled"`
	DiskUtilizedLowerBoundPct        float64 `json:"diskUtilizedLowerBoundPct"` // -> mapped to ActivationLowerBound
	DiskUtilizedUpperBoundPct        float64 `json:"diskUtilizedUpperBoundPct"` // -> mapped to ActivationUpperBound
	DiskUtilizedActivationLowerBound float64 `json:"diskUtilizedActivationLowerBound"`
	DiskUtilizedActivationUpperBound float64 `json:"diskUtilizedActivationUpperBound"`
}

func (o ResourceBalancingStepOpts) Validate() error {
	// Avoid zero-division during min-max scaling.
	if o.CPUUtilizedLowerBoundPct == o.CPUUtilizedUpperBoundPct {
		return errors.New("cpuUtilizedLowerBound and cpuUtilizedUpperBound must not be equal")
	}
	if o.RAMUtilizedLowerBoundPct == o.RAMUtilizedUpperBoundPct {
		return errors.New("ramUtilizedLowerBound and ramUtilizedUpperBound must not be equal")
	}
	if o.DiskUtilizedLowerBoundPct == o.DiskUtilizedUpperBoundPct {
		return errors.New("diskUtilizedLowerBound and diskUtilizedUpperBound must not be equal")
	}
	return nil
}

// Step to balance VMs on hosts based on the host's available resources.
type ResourceBalancingStep struct {
	// BaseStep is a helper struct that provides common functionality for all steps.
	scheduler.BaseStep[api.ExternalSchedulerRequest, ResourceBalancingStepOpts]
}

// Get the name of this step, used for identification in config, logs, metrics, etc.
func (s *ResourceBalancingStep) GetName() string {
	return "shared_resource_balancing"
}

// Pack VMs on hosts based on their flavor.
func (s *ResourceBalancingStep) Run(traceLog *slog.Logger, request api.ExternalSchedulerRequest) (*scheduler.StepResult, error) {
	result := s.PrepareResult(request)
	if s.Options.CPUEnabled {
		result.Statistics["cpu utilized"] = s.PrepareStats(request, "%")
	}
	if s.Options.RAMEnabled {
		result.Statistics["ram utilized"] = s.PrepareStats(request, "%")
	}
	if s.Options.DiskEnabled {
		result.Statistics["disk utilized"] = s.PrepareStats(request, "%")
	}

	var hostUtilizations []shared.HostUtilization
	group := "scheduler-nova"
	if _, err := s.DB.SelectTimed(
		group, &hostUtilizations, "SELECT * FROM "+shared.HostUtilization{}.TableName(),
	); err != nil {
		return nil, err
	}
	for _, hostUtilization := range hostUtilizations {
		// Only modify the weight if the host is in the scenario.
		if _, ok := result.Activations[hostUtilization.ComputeHost]; !ok {
			continue
		}
		activationCPU := s.NoEffect()
		if s.Options.CPUEnabled {
			activationCPU = scheduler.MinMaxScale(
				hostUtilization.VCPUsUtilizedPct,
				s.Options.CPUUtilizedLowerBoundPct,
				s.Options.CPUUtilizedUpperBoundPct,
				s.Options.CPUUtilizedActivationLowerBound,
				s.Options.CPUUtilizedActivationUpperBound,
			)
			result.
				Statistics["cpu utilized"].
				Subjects[hostUtilization.ComputeHost] = hostUtilization.VCPUsUtilizedPct
		}
		activationRAM := s.NoEffect()
		if s.Options.RAMEnabled {
			activationRAM = scheduler.MinMaxScale(
				hostUtilization.RAMUtilizedPct,
				s.Options.RAMUtilizedLowerBoundPct,
				s.Options.RAMUtilizedUpperBoundPct,
				s.Options.RAMUtilizedActivationLowerBound,
				s.Options.RAMUtilizedActivationUpperBound,
			)
			result.
				Statistics["ram utilized"].
				Subjects[hostUtilization.ComputeHost] = hostUtilization.RAMUtilizedPct
		}
		activationDisk := s.NoEffect()
		if s.Options.DiskEnabled {
			activationDisk = scheduler.MinMaxScale(
				hostUtilization.DiskUtilizedPct,
				s.Options.DiskUtilizedLowerBoundPct,
				s.Options.DiskUtilizedUpperBoundPct,
				s.Options.DiskUtilizedActivationLowerBound,
				s.Options.DiskUtilizedActivationUpperBound,
			)
			result.
				Statistics["disk utilized"].
				Subjects[hostUtilization.ComputeHost] = hostUtilization.DiskUtilizedPct
		}
		result.Activations[hostUtilization.ComputeHost] = activationCPU + activationRAM + activationDisk
	}
	return result, nil
}
