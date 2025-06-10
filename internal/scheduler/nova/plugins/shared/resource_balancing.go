// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package shared

import (
	"errors"
	"log/slog"

	"github.com/cobaltcore-dev/cortex/internal/extractor/plugins/shared"
	"github.com/cobaltcore-dev/cortex/internal/scheduler"
	"github.com/cobaltcore-dev/cortex/internal/scheduler/nova/api"
	"github.com/cobaltcore-dev/cortex/internal/scheduler/nova/plugins"
)

// Options for the scheduling step, given through the step config in the service yaml file.
type ResourceBalancingStepOpts struct {
	CPUEnabled                  bool    `json:"cpuEnabled"`
	CPUFreeLowerBoundPct        float64 `json:"cpuFreeLowerBoundPct"` // -> mapped to ActivationLowerBound
	CPUFreeUpperBoundPct        float64 `json:"cpuFreeUpperBoundPct"` // -> mapped to ActivationUpperBound
	CPUFreeActivationLowerBound float64 `json:"cpuFreeActivationLowerBound"`
	CPUFreeActivationUpperBound float64 `json:"cpuFreeActivationUpperBound"`

	RAMEnabled                  bool    `json:"ramEnabled"`
	RAMFreeLowerBoundPct        float64 `json:"ramFreeLowerBoundPct"` // -> mapped to ActivationLowerBound
	RAMFreeUpperBoundPct        float64 `json:"ramFreeUpperBoundPct"` // -> mapped to ActivationUpperBound
	RAMFreeActivationLowerBound float64 `json:"ramFreeActivationLowerBound"`
	RAMFreeActivationUpperBound float64 `json:"ramFreeActivationUpperBound"`

	DiskEnabled                  bool    `json:"diskEnabled"`
	DiskFreeLowerBoundPct        float64 `json:"diskFreeLowerBoundPct"` // -> mapped to ActivationLowerBound
	DiskFreeUpperBoundPct        float64 `json:"diskFreeUpperBoundPct"` // -> mapped to ActivationUpperBound
	DiskFreeActivationLowerBound float64 `json:"diskFreeActivationLowerBound"`
	DiskFreeActivationUpperBound float64 `json:"diskFreeActivationUpperBound"`
}

func (o ResourceBalancingStepOpts) Validate() error {
	// Avoid zero-division during min-max scaling.
	if o.CPUFreeLowerBoundPct == o.CPUFreeUpperBoundPct {
		return errors.New("cpuFreeLowerBound and cpuFreeUpperBound must not be equal")
	}
	if o.RAMFreeLowerBoundPct == o.RAMFreeUpperBoundPct {
		return errors.New("ramFreeLowerBound and ramFreeUpperBound must not be equal")
	}
	if o.DiskFreeLowerBoundPct == o.DiskFreeUpperBoundPct {
		return errors.New("diskFreeLowerBound and diskFreeUpperBound must not be equal")
	}
	return nil
}

// Step to balance VMs on hosts based on the host's available resources.
type ResourceBalancingStep struct {
	// BaseStep is a helper struct that provides common functionality for all steps.
	plugins.BaseStep[ResourceBalancingStepOpts]
}

// Get the name of this step, used for identification in config, logs, metrics, etc.
func (s *ResourceBalancingStep) GetName() string {
	return "shared_resource_balancing"
}

// Pack VMs on hosts based on their flavor.
func (s *ResourceBalancingStep) Run(traceLog *slog.Logger, request api.Request) (*plugins.StepResult, error) {
	result := s.PrepareResult(request)
	if s.Options.CPUEnabled {
		result.Statistics["cpu resource usage"] = s.PrepareStats(request, "%")
	}
	if s.Options.RAMEnabled {
		result.Statistics["ram resource usage"] = s.PrepareStats(request, "%")
	}
	if s.Options.DiskEnabled {
		result.Statistics["disk resource usage"] = s.PrepareStats(request, "%")
	}

	spec := request.GetSpec()
	if spec.Data.NInstances > 1 {
		return result, nil
	}
	var hostSpaces []shared.HostSpace
	if _, err := s.DB.Select(
		&hostSpaces, "SELECT * FROM "+shared.HostSpace{}.TableName(),
	); err != nil {
		return nil, err
	}
	for _, hostSpace := range hostSpaces {
		// Only modify the weight if the host is in the scenario.
		if _, ok := result.Activations[hostSpace.ComputeHost]; !ok {
			continue
		}
		activationCPU := s.NoEffect()
		if s.Options.CPUEnabled {
			activationCPU = scheduler.MinMaxScale(
				hostSpace.VCPUsLeftPct,
				s.Options.CPUFreeLowerBoundPct,
				s.Options.CPUFreeUpperBoundPct,
				s.Options.CPUFreeActivationLowerBound,
				s.Options.CPUFreeActivationUpperBound,
			)
			result.
				Statistics["cpu resource usage"].
				Hosts[hostSpace.ComputeHost] = hostSpace.VCPUsLeftPct
		}
		activationRAM := s.NoEffect()
		if s.Options.RAMEnabled {
			activationRAM = scheduler.MinMaxScale(
				hostSpace.RAMLeftPct,
				s.Options.RAMFreeLowerBoundPct,
				s.Options.RAMFreeUpperBoundPct,
				s.Options.RAMFreeActivationLowerBound,
				s.Options.RAMFreeActivationUpperBound,
			)
			result.
				Statistics["ram resource usage"].
				Hosts[hostSpace.ComputeHost] = hostSpace.RAMLeftPct
		}
		activationDisk := s.NoEffect()
		if s.Options.DiskEnabled {
			activationDisk = scheduler.MinMaxScale(
				hostSpace.DiskLeftPct,
				s.Options.DiskFreeLowerBoundPct,
				s.Options.DiskFreeUpperBoundPct,
				s.Options.DiskFreeActivationLowerBound,
				s.Options.DiskFreeActivationUpperBound,
			)
			result.
				Statistics["disk resource usage"].
				Hosts[hostSpace.ComputeHost] = hostSpace.DiskLeftPct
		}
		result.Activations[hostSpace.ComputeHost] = activationCPU + activationRAM + activationDisk
	}
	return result, nil
}
