// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package shared

import (
	"errors"
	"log/slog"
	"slices"

	"github.com/cobaltcore-dev/cortex/extractor/plugins/shared"
	"github.com/cobaltcore-dev/cortex/internal/scheduler/api"
	"github.com/cobaltcore-dev/cortex/internal/scheduler/plugins"
)

// Options for the scheduling step, given through the step config in the service yaml file.
type FlavorBinpackingStepOpts struct {
	// Flavor names to consider for the binpacking.
	// If this list is empty, all flavors are considered.
	Flavors []string `json:"flavors"`

	CPUEnabled                  bool    `json:"cpuEnabled"`
	CPUFreeLowerBound           float64 `json:"cpuFreeLowerBound"` // -> mapped to ActivationLowerBound
	CPUFreeUpperBound           float64 `json:"cpuFreeUpperBound"` // -> mapped to ActivationUpperBound
	CPUFreeActivationLowerBound float64 `json:"cpuFreeActivationLowerBound"`
	CPUFreeActivationUpperBound float64 `json:"cpuFreeActivationUpperBound"`

	RAMEnabled                  bool    `json:"ramEnabled"`
	RAMFreeLowerBound           float64 `json:"ramFreeLowerBound"` // -> mapped to ActivationLowerBound
	RAMFreeUpperBound           float64 `json:"ramFreeUpperBound"` // -> mapped to ActivationUpperBound
	RAMFreeActivationLowerBound float64 `json:"ramFreeActivationLowerBound"`
	RAMFreeActivationUpperBound float64 `json:"ramFreeActivationUpperBound"`

	DiskEnabled                  bool    `json:"diskEnabled"`
	DiskFreeLowerBound           float64 `json:"diskFreeLowerBound"` // -> mapped to ActivationLowerBound
	DiskFreeUpperBound           float64 `json:"diskFreeUpperBound"` // -> mapped to ActivationUpperBound
	DiskFreeActivationLowerBound float64 `json:"diskFreeActivationLowerBound"`
	DiskFreeActivationUpperBound float64 `json:"diskFreeActivationUpperBound"`
}

func (o FlavorBinpackingStepOpts) Validate() error {
	// Avoid zero-division during min-max scaling.
	if o.CPUFreeLowerBound == o.CPUFreeUpperBound {
		return errors.New("cpuFreeLowerBound and cpuFreeUpperBound must not be equal")
	}
	if o.RAMFreeLowerBound == o.RAMFreeUpperBound {
		return errors.New("ramFreeLowerBound and ramFreeUpperBound must not be equal")
	}
	if o.DiskFreeLowerBound == o.DiskFreeUpperBound {
		return errors.New("diskFreeLowerBound and diskFreeUpperBound must not be equal")
	}
	return nil
}

// Step to pack VMs on hosts based on their flavor.
type FlavorBinpackingStep struct {
	// BaseStep is a helper struct that provides common functionality for all steps.
	plugins.BaseStep[FlavorBinpackingStepOpts]
}

// Get the name of this step, used for identification in config, logs, metrics, etc.
func (s *FlavorBinpackingStep) GetName() string {
	return "shared_flavor_binpacking"
}

// Pack VMs on hosts based on their flavor.
func (s *FlavorBinpackingStep) Run(traceLog *slog.Logger, request api.Request) (*plugins.StepResult, error) {
	result := s.PrepareResult(request)
	if s.Options.CPUEnabled {
		result.Statistics["cpu free after flavor placement"] = s.PrepareStats(request, "vCPUs")
	}
	if s.Options.RAMEnabled {
		result.Statistics["ram free after flavor placement"] = s.PrepareStats(request, "MB")
	}
	if s.Options.DiskEnabled {
		result.Statistics["disk free after flavor placement"] = s.PrepareStats(request, "GB")
	}

	spec := request.GetSpec()
	if spec.Data.NInstances > 1 {
		return result, nil
	}
	flavorName := spec.Data.Flavor.Data.Name
	if len(s.Options.Flavors) > 0 && !slices.Contains(s.Options.Flavors, flavorName) {
		// Skip this step if the flavor is not in the list of flavors to consider.
		return result, nil
	}

	var flavorHostSpaces []shared.FlavorHostSpace
	if _, err := s.DB.Select(&flavorHostSpaces, `SELECT *
		FROM feature_flavor_host_space
		WHERE
			flavor_id = :id AND
			ram_left_mb >= 0 AND
			vcpus_left >= 0 AND
			disk_left_gb >= 0
	`, map[string]any{"id": spec.Data.Flavor.Data.FlavorID}); err != nil {
		return nil, err
	}

	for _, f := range flavorHostSpaces {
		// Only modify the weight if the host is in the scenario.
		if _, ok := result.Activations[f.ComputeHost]; !ok {
			continue
		}
		activationCPU := s.NoEffect()
		if s.Options.CPUEnabled {
			activationCPU = plugins.MinMaxScale(
				float64(f.VCPUsLeft),
				s.Options.CPUFreeLowerBound,
				s.Options.CPUFreeUpperBound,
				s.Options.CPUFreeActivationLowerBound,
				s.Options.CPUFreeActivationUpperBound,
			)
			result.Statistics["cpu free after flavor placement"].Hosts[f.ComputeHost] = float64(f.VCPUsLeft)
		}
		activationRAM := s.NoEffect()
		if s.Options.RAMEnabled {
			activationRAM = plugins.MinMaxScale(
				float64(f.RAMLeftMB),
				s.Options.RAMFreeLowerBound,
				s.Options.RAMFreeUpperBound,
				s.Options.RAMFreeActivationLowerBound,
				s.Options.RAMFreeActivationUpperBound,
			)
			result.Statistics["ram free after flavor placement"].Hosts[f.ComputeHost] = float64(f.RAMLeftMB)
		}
		activationDisk := s.NoEffect()
		if s.Options.DiskEnabled {
			activationDisk = plugins.MinMaxScale(
				float64(f.DiskLeftGB),
				s.Options.DiskFreeLowerBound,
				s.Options.DiskFreeUpperBound,
				s.Options.DiskFreeActivationLowerBound,
				s.Options.DiskFreeActivationUpperBound,
			)
			result.Statistics["disk free after flavor placement"].Hosts[f.ComputeHost] = float64(f.DiskLeftGB)
		}
		result.Activations[f.ComputeHost] = activationCPU + activationRAM + activationDisk
	}
	return result, nil
}
