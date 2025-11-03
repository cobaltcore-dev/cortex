// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package shared

import (
	"errors"
	"log/slog"

	"github.com/cobaltcore-dev/cortex/knowledge/api/features/shared"
	api "github.com/cobaltcore-dev/cortex/scheduling/api/delegation/nova"
	scheduling "github.com/cobaltcore-dev/cortex/scheduling/internal/lib"
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

	CPUAfterEnabled                      bool    `json:"cpuAfterEnabled"`
	CPUUtilizedAfterLowerBoundPct        float64 `json:"cpuUtilizedAfterLowerBoundPct"` // -> mapped to ActivationLowerBound
	CPUUtilizedAfterUpperBoundPct        float64 `json:"cpuUtilizedAfterUpperBoundPct"` // -> mapped to ActivationUpperBound
	CPUUtilizedAfterActivationLowerBound float64 `json:"cpuUtilizedAfterActivationLowerBound"`
	CPUUtilizedAfterActivationUpperBound float64 `json:"cpuUtilizedAfterActivationUpperBound"`

	RAMAfterEnabled                      bool    `json:"ramAfterEnabled"`
	RAMUtilizedAfterLowerBoundPct        float64 `json:"ramUtilizedAfterLowerBoundPct"` // -> mapped to ActivationLowerBound
	RAMUtilizedAfterUpperBoundPct        float64 `json:"ramUtilizedAfterUpperBoundPct"` // -> mapped to ActivationUpperBound
	RAMUtilizedAfterActivationLowerBound float64 `json:"ramUtilizedAfterActivationLowerBound"`
	RAMUtilizedAfterActivationUpperBound float64 `json:"ramUtilizedAfterActivationUpperBound"`

	DiskAfterEnabled                      bool    `json:"diskAfterEnabled"`
	DiskUtilizedAfterLowerBoundPct        float64 `json:"diskUtilizedAfterLowerBoundPct"` // -> mapped to ActivationLowerBound
	DiskUtilizedAfterUpperBoundPct        float64 `json:"diskUtilizedAfterUpperBoundPct"` // -> mapped to ActivationUpperBound
	DiskUtilizedAfterActivationLowerBound float64 `json:"diskUtilizedAfterActivationLowerBound"`
	DiskUtilizedAfterActivationUpperBound float64 `json:"diskUtilizedAfterActivationUpperBound"`
}

func (o ResourceBalancingStepOpts) Validate() error {
	// Avoid zero-division during min-max scaling.
	if o.CPUEnabled {
		if o.CPUUtilizedLowerBoundPct == o.CPUUtilizedUpperBoundPct {
			return errors.New("cpuUtilizedLowerBound and cpuUtilizedUpperBound must not be equal")
		}
	}
	if o.RAMEnabled {
		if o.RAMUtilizedLowerBoundPct == o.RAMUtilizedUpperBoundPct {
			return errors.New("ramUtilizedLowerBound and ramUtilizedUpperBound must not be equal")
		}
	}
	if o.DiskEnabled {
		if o.DiskUtilizedLowerBoundPct == o.DiskUtilizedUpperBoundPct {
			return errors.New("diskUtilizedLowerBound and diskUtilizedUpperBound must not be equal")
		}
	}
	if o.CPUAfterEnabled {
		if o.CPUUtilizedAfterLowerBoundPct == o.CPUUtilizedAfterUpperBoundPct {
			return errors.New("cpuUtilizedAfterLowerBound and cpuUtilizedAfterUpperBound must not be equal")
		}
	}
	if o.RAMAfterEnabled {
		if o.RAMUtilizedAfterLowerBoundPct == o.RAMUtilizedAfterUpperBoundPct {
			return errors.New("ramUtilizedAfterLowerBound and ramUtilizedAfterUpperBound must not be equal")
		}
	}
	if o.DiskAfterEnabled {
		if o.DiskUtilizedAfterLowerBoundPct == o.DiskUtilizedAfterUpperBoundPct {
			return errors.New("diskUtilizedAfterLowerBound and diskUtilizedAfterUpperBound must not be equal")
		}
	}
	return nil
}

// Step to balance VMs on hosts based on the host's available resources.
type ResourceBalancingStep struct {
	// BaseStep is a helper struct that provides common functionality for all steps.
	scheduling.BaseStep[api.ExternalSchedulerRequest, ResourceBalancingStepOpts]
}

// Pack VMs on hosts based on their flavor.
func (s *ResourceBalancingStep) Run(traceLog *slog.Logger, request api.ExternalSchedulerRequest) (*scheduling.StepResult, error) {
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
	if s.Options.CPUAfterEnabled {
		result.Statistics["cpu utilized after"] = s.PrepareStats(request, "%")
	}
	if s.Options.RAMAfterEnabled {
		result.Statistics["ram utilized after"] = s.PrepareStats(request, "%")
	}
	if s.Options.DiskAfterEnabled {
		result.Statistics["disk utilized after"] = s.PrepareStats(request, "%")
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
			activationCPU = scheduling.MinMaxScale(
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
			activationRAM = scheduling.MinMaxScale(
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
			activationDisk = scheduling.MinMaxScale(
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
		activationAfterCPU := s.NoEffect()
		if s.Options.CPUAfterEnabled {
			after := hostUtilization.VCPUsUtilizedPct -
				(float64(request.Spec.Data.Flavor.Data.VCPUs) /
					hostUtilization.TotalVCPUsAllocatable * 100)
			activationAfterCPU = scheduling.MinMaxScale(
				after,
				s.Options.CPUUtilizedAfterLowerBoundPct,
				s.Options.CPUUtilizedAfterUpperBoundPct,
				s.Options.CPUUtilizedAfterActivationLowerBound,
				s.Options.CPUUtilizedAfterActivationUpperBound,
			)
			result.
				Statistics["cpu utilized after"].
				Subjects[hostUtilization.ComputeHost] = after
		}
		activationAfterRAM := s.NoEffect()
		if s.Options.RAMAfterEnabled {
			after := hostUtilization.RAMUtilizedPct -
				(float64(request.Spec.Data.Flavor.Data.MemoryMB) /
					hostUtilization.TotalRAMAllocatableMB * 100)
			activationAfterRAM = scheduling.MinMaxScale(
				after,
				s.Options.RAMUtilizedAfterLowerBoundPct,
				s.Options.RAMUtilizedAfterUpperBoundPct,
				s.Options.RAMUtilizedAfterActivationLowerBound,
				s.Options.RAMUtilizedAfterActivationUpperBound,
			)
			result.
				Statistics["ram utilized after"].
				Subjects[hostUtilization.ComputeHost] = after
		}
		activationAfterDisk := s.NoEffect()
		if s.Options.DiskAfterEnabled {
			after := hostUtilization.DiskUtilizedPct -
				(float64(request.Spec.Data.Flavor.Data.RootGB) /
					hostUtilization.TotalDiskAllocatableGB * 100)
			activationAfterDisk = scheduling.MinMaxScale(
				after,
				s.Options.DiskUtilizedAfterLowerBoundPct,
				s.Options.DiskUtilizedAfterUpperBoundPct,
				s.Options.DiskUtilizedAfterActivationLowerBound,
				s.Options.DiskUtilizedAfterActivationUpperBound,
			)
			result.
				Statistics["disk utilized after"].
				Subjects[hostUtilization.ComputeHost] = after
		}
		result.Activations[hostUtilization.ComputeHost] = 0 +
			activationCPU + activationRAM + activationDisk +
			activationAfterCPU + activationAfterRAM + activationAfterDisk
	}
	return result, nil
}
