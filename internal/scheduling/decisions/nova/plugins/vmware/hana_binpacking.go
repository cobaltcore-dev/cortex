// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package vmware

import (
	"errors"
	"log/slog"
	"strings"

	api "github.com/cobaltcore-dev/cortex/api/delegation/nova"
	"github.com/cobaltcore-dev/cortex/internal/knowledge/extractor/plugins/shared"
	scheduling "github.com/cobaltcore-dev/cortex/internal/scheduling/lib"
)

// Options for the scheduling step, given through the step config in the service yaml file.
type HanaBinpackingStepOpts struct {
	RAMUtilizedAfterLowerBoundPct        float64 `json:"ramUtilizedAfterLowerBoundPct"` // -> mapped to ActivationLowerBound
	RAMUtilizedAfterUpperBoundPct        float64 `json:"ramUtilizedAfterUpperBoundPct"` // -> mapped to ActivationUpperBound
	RAMUtilizedAfterActivationLowerBound float64 `json:"ramUtilizedAfterActivationLowerBound"`
	RAMUtilizedAfterActivationUpperBound float64 `json:"ramUtilizedAfterActivationUpperBound"`
}

func (o HanaBinpackingStepOpts) Validate() error {
	// Avoid zero-division during min-max scaling.
	if o.RAMUtilizedAfterLowerBoundPct == o.RAMUtilizedAfterUpperBoundPct {
		return errors.New("ramUtilizedAfterLowerBound and ramUtilizedAfterUpperBound must not be equal")
	}
	return nil
}

// Step to balance VMs on hosts based on the host's available resources.
type HanaBinpackingStep struct {
	// BaseStep is a helper struct that provides common functionality for all steps.
	scheduling.BaseStep[api.ExternalSchedulerRequest, HanaBinpackingStepOpts]
}

// Pack VMs on hosts based on their flavor.
func (s *HanaBinpackingStep) Run(traceLog *slog.Logger, request api.ExternalSchedulerRequest) (*scheduling.StepResult, error) {
	result := s.PrepareResult(request)
	// Don't execute the step for non-hana flavors.
	if !strings.Contains(request.Spec.Data.Flavor.Data.Name, "hana") {
		slog.Debug("Skipping hana binpacking step for non-HANA flavor", "flavor", request.Spec.Data.Flavor.Data.Name)
		return result, nil
	}
	if !request.VMware {
		slog.Debug("Skipping hana binpacking step for non-VMware VM")
		return result, nil
	}

	result.Statistics["ram utilized after"] = s.PrepareStats(request, "%")

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
		after := hostUtilization.RAMUtilizedPct -
			(float64(request.Spec.Data.Flavor.Data.MemoryMB) /
				hostUtilization.TotalRAMAllocatableMB * 100)
		result.
			Statistics["ram utilized after"].
			Subjects[hostUtilization.ComputeHost] = after
		result.Activations[hostUtilization.ComputeHost] = scheduling.MinMaxScale(
			after,
			s.Options.RAMUtilizedAfterLowerBoundPct,
			s.Options.RAMUtilizedAfterUpperBoundPct,
			s.Options.RAMUtilizedAfterActivationLowerBound,
			s.Options.RAMUtilizedAfterActivationUpperBound,
		)
	}

	// Fetch the host capabilities.
	// Note: due to the vmware spec selector, it is expected that
	// this step is only executed for VMware hosts.
	var hostCapabilities []shared.HostCapabilities
	if _, err := s.DB.Select(
		&hostCapabilities, "SELECT * FROM "+shared.HostCapabilities{}.TableName(),
	); err != nil {
		return nil, err
	}
	capabilityByHost := make(map[string]shared.HostCapabilities, len(request.Hosts))
	for _, hostCapability := range hostCapabilities {
		capabilityByHost[hostCapability.ComputeHost] = hostCapability
	}
	for _, host := range request.Hosts {
		capability, ok := capabilityByHost[host.ComputeHost]
		if !ok {
			slog.Warn("No host capabilities found for host", "host", host.ComputeHost)
			result.Activations[host.ComputeHost] = s.NoEffect()
			continue
		}
		if !strings.Contains(capability.Traits, "HANA_EXCLUSIVE") {
			slog.Debug("Skipping hana binpacking for host without HANA_EXCLUSIVE trait", "host", host.ComputeHost)
			result.Activations[host.ComputeHost] = s.NoEffect()
			continue
		}
	}

	return result, nil
}
