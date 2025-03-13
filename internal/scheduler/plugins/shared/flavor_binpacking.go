// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package shared

import (
	"errors"

	"github.com/cobaltcore-dev/cortex/internal/features/plugins/shared"
	"github.com/cobaltcore-dev/cortex/internal/scheduler/api"
	"github.com/cobaltcore-dev/cortex/internal/scheduler/plugins"
)

// Options for the scheduling step, given through the step config in the service yaml file.
type FlavorBinpackingStepOpts struct {
	CPUEnabled                  bool    `yaml:"cpuEnabled"`
	CPUFreeLowerBound           float64 `yaml:"cpuFreeLowerBound"` // -> mapped to ActivationLowerBound
	CPUFreeUpperBound           float64 `yaml:"cpuFreeUpperBound"` // -> mapped to ActivationUpperBound
	CPUFreeActivationLowerBound float64 `yaml:"cpuFreeActivationLowerBound"`
	CPUFreeActivationUpperBound float64 `yaml:"cpuFreeActivationUpperBound"`

	RAMEnabled                  bool    `yaml:"ramEnabled"`
	RAMFreeLowerBound           float64 `yaml:"ramFreeLowerBound"` // -> mapped to ActivationLowerBound
	RAMFreeUpperBound           float64 `yaml:"ramFreeUpperBound"` // -> mapped to ActivationUpperBound
	RAMFreeActivationLowerBound float64 `yaml:"ramFreeActivationLowerBound"`
	RAMFreeActivationUpperBound float64 `yaml:"ramFreeActivationUpperBound"`

	DiskEnabled                  bool    `yaml:"diskEnabled"`
	DiskFreeLowerBound           float64 `yaml:"diskFreeLowerBound"` // -> mapped to ActivationLowerBound
	DiskFreeUpperBound           float64 `yaml:"diskFreeUpperBound"` // -> mapped to ActivationUpperBound
	DiskFreeActivationLowerBound float64 `yaml:"diskFreeActivationLowerBound"`
	DiskFreeActivationUpperBound float64 `yaml:"diskFreeActivationUpperBound"`
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
func (s *FlavorBinpackingStep) Run(request api.Request) (map[string]float64, error) {
	activations := s.BaseStep.BaseActivations(request)

	if request.Spec.Data.NInstances > 1 {
		return activations, nil
	}

	var flavorHostSpaces []shared.FlavorHostSpace
	if _, err := s.DB.Select(&flavorHostSpaces, `SELECT *
		FROM feature_flavor_host_space
		WHERE
			flavor_id = :id AND
			ram_left_mb >= 0 AND
			vcpus_left >= 0 AND
			disk_left_gb >= 0
	`, map[string]any{"id": request.Spec.Data.Flavor.Data.FlavorID}); err != nil {
		return nil, err
	}

	for _, f := range flavorHostSpaces {
		// Only modify the weight if the host is in the scenario.
		if _, ok := activations[f.ComputeHost]; !ok {
			continue
		}
		activationCPU := s.ActivationFunction.NoEffect()
		if s.Options.CPUEnabled {
			activationCPU = plugins.MinMaxScale(
				float64(f.VCPUsLeft),
				s.Options.CPUFreeLowerBound,
				s.Options.CPUFreeUpperBound,
				s.Options.CPUFreeActivationLowerBound,
				s.Options.CPUFreeActivationUpperBound,
			)
		}
		activationRAM := s.ActivationFunction.NoEffect()
		if s.Options.RAMEnabled {
			activationRAM = plugins.MinMaxScale(
				float64(f.RAMLeftMB),
				s.Options.RAMFreeLowerBound,
				s.Options.RAMFreeUpperBound,
				s.Options.RAMFreeActivationLowerBound,
				s.Options.RAMFreeActivationUpperBound,
			)
		}
		activationDisk := s.ActivationFunction.NoEffect()
		if s.Options.DiskEnabled {
			activationDisk = plugins.MinMaxScale(
				float64(f.DiskLeftGB),
				s.Options.DiskFreeLowerBound,
				s.Options.DiskFreeUpperBound,
				s.Options.DiskFreeActivationLowerBound,
				s.Options.DiskFreeActivationUpperBound,
			)
		}
		activations[f.ComputeHost] = activationCPU + activationRAM + activationDisk
	}
	return activations, nil
}
