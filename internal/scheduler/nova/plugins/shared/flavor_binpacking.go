// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package shared

import (
	"errors"
	"log/slog"
	"slices"
	"strings"

	"github.com/cobaltcore-dev/cortex/internal/extractor/plugins/shared"
	"github.com/cobaltcore-dev/cortex/internal/scheduler"
	"github.com/cobaltcore-dev/cortex/internal/scheduler/nova/api"
	"github.com/cobaltcore-dev/cortex/internal/scheduler/nova/plugins"
)

// Options for the scheduling step, given through the step config in the service yaml file.
type FlavorBinpackingStepOpts struct {
	// Flavor names to consider for the binpacking.
	// If this list is empty, all flavors are considered.
	Flavors []string `json:"flavors"`

	// Traits of the hypervisor resource provider to consider
	// for binpacking. If empty, all traits are considered.
	Traits []string `json:"traits"`

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

	var hostSpaces []shared.HostSpace
	if _, err := s.DB.Select(
		&hostSpaces, "SELECT * FROM "+shared.HostSpace{}.TableName(),
	); err != nil {
		return nil, err
	}

	// Note: we could technically use a LIKE %TRAIT1% OR LIKE %TRAIT2% query here,
	// but for now we want to keep it simple. This should not return too many hosts.
	var hostTraits []shared.HostTraits
	if _, err := s.DB.Select(
		&hostTraits, "SELECT * FROM "+shared.HostTraits{}.TableName(),
	); err != nil {
		return nil, err
	}

	traitsByHost := make(map[string]string, len(hostTraits))
	for _, hostTrait := range hostTraits {
		// .Traits is a comma-separated list of traits
		traitsByHost[hostTrait.ComputeHost] = hostTrait.Traits
	}

	skippedDueToTraits := 0
	for _, hostSpace := range hostSpaces {
		// Only modify the weight if the host is in the scenario.
		if _, ok := result.Activations[hostSpace.ComputeHost]; !ok {
			continue
		}
		traits, ok := traitsByHost[hostSpace.ComputeHost]
		if !ok && len(s.Options.Traits) > 0 {
			// If the host does not have traits, skip it if we are filtering by traits.
			skippedDueToTraits++
			continue
		}
		for _, trait := range s.Options.Traits {
			if !strings.Contains(traits, trait) {
				// If the host does not have the required trait, skip it.
				skippedDueToTraits++
				continue
			}
		}
		// Host has the required traits, so we can consider it for binpacking.

		activationCPU := s.NoEffect()
		if s.Options.CPUEnabled {
			vcpusLeftAfterPlacement := float64(hostSpace.VCPUsLeft - spec.Data.Flavor.Data.VCPUs)
			if vcpusLeftAfterPlacement < 0 {
				// If the host does not have enough vCPUs left, skip it.
				continue
			}
			activationCPU = scheduler.MinMaxScale(
				vcpusLeftAfterPlacement,
				s.Options.CPUFreeLowerBound,
				s.Options.CPUFreeUpperBound,
				s.Options.CPUFreeActivationLowerBound,
				s.Options.CPUFreeActivationUpperBound,
			)
			result.
				Statistics["cpu free after flavor placement"].
				Hosts[hostSpace.ComputeHost] = vcpusLeftAfterPlacement
		}
		activationRAM := s.NoEffect()
		if s.Options.RAMEnabled {
			ramLeftAfterPlacement := float64(hostSpace.RAMLeftMB - spec.Data.Flavor.Data.MemoryMB)
			if ramLeftAfterPlacement < 0 {
				// If the host does not have enough RAM left, skip it.
				continue
			}
			activationRAM = scheduler.MinMaxScale(
				ramLeftAfterPlacement,
				s.Options.RAMFreeLowerBound,
				s.Options.RAMFreeUpperBound,
				s.Options.RAMFreeActivationLowerBound,
				s.Options.RAMFreeActivationUpperBound,
			)
			result.
				Statistics["ram free after flavor placement"].
				Hosts[hostSpace.ComputeHost] = ramLeftAfterPlacement
		}
		activationDisk := s.NoEffect()
		if s.Options.DiskEnabled {
			diskLeftAfterPlacement := float64(hostSpace.DiskLeftGB - spec.Data.Flavor.Data.RootDiskGB)
			if diskLeftAfterPlacement < 0 {
				// If the host does not have enough disk left, skip it.
				continue
			}
			activationDisk = scheduler.MinMaxScale(
				diskLeftAfterPlacement,
				s.Options.DiskFreeLowerBound,
				s.Options.DiskFreeUpperBound,
				s.Options.DiskFreeActivationLowerBound,
				s.Options.DiskFreeActivationUpperBound,
			)
			result.
				Statistics["disk free after flavor placement"].
				Hosts[hostSpace.ComputeHost] = diskLeftAfterPlacement
		}
		result.Activations[hostSpace.ComputeHost] = activationCPU + activationRAM + activationDisk
	}
	if skippedDueToTraits > 0 {
		traceLog.Info("binpacking: skipped hosts due to trait scope",
			"skipped", skippedDueToTraits,
			"traits", s.Options.Traits,
			"total", len(request.GetHosts()),
		)
	}
	return result, nil
}
