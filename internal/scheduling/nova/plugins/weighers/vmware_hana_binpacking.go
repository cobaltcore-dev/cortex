// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package weighers

import (
	"context"
	"errors"
	"log/slog"
	"strings"

	api "github.com/cobaltcore-dev/cortex/api/delegation/nova"
	"github.com/cobaltcore-dev/cortex/api/v1alpha1"
	"github.com/cobaltcore-dev/cortex/internal/knowledge/extractor/plugins/compute"
	"github.com/cobaltcore-dev/cortex/internal/scheduling/lib"
	corev1 "k8s.io/api/core/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// Options for the scheduling step, given through the step config in the service yaml file.
type VMwareHanaBinpackingStepOpts struct {
	RAMUtilizedAfterLowerBoundPct        float64 `json:"ramUtilizedAfterLowerBoundPct"` // -> mapped to ActivationLowerBound
	RAMUtilizedAfterUpperBoundPct        float64 `json:"ramUtilizedAfterUpperBoundPct"` // -> mapped to ActivationUpperBound
	RAMUtilizedAfterActivationLowerBound float64 `json:"ramUtilizedAfterActivationLowerBound"`
	RAMUtilizedAfterActivationUpperBound float64 `json:"ramUtilizedAfterActivationUpperBound"`
}

func (o VMwareHanaBinpackingStepOpts) Validate() error {
	// Avoid zero-division during min-max scaling.
	if o.RAMUtilizedAfterLowerBoundPct == o.RAMUtilizedAfterUpperBoundPct {
		return errors.New("ramUtilizedAfterLowerBound and ramUtilizedAfterUpperBound must not be equal")
	}
	return nil
}

// Step to balance VMs on hosts based on the host's available resources.
type VMwareHanaBinpackingStep struct {
	// BaseStep is a helper struct that provides common functionality for all steps.
	lib.BaseWeigher[api.ExternalSchedulerRequest, VMwareHanaBinpackingStepOpts]
}

// Initialize the step and validate that all required knowledges are ready.
func (s *VMwareHanaBinpackingStep) Init(ctx context.Context, client client.Client, weigher v1alpha1.WeigherSpec) error {
	if err := s.BaseWeigher.Init(ctx, client, weigher); err != nil {
		return err
	}
	if err := s.CheckKnowledges(ctx,
		corev1.ObjectReference{Name: "host-utilization"},
		corev1.ObjectReference{Name: "host-capabilities"},
	); err != nil {
		return err
	}
	return nil
}

// Pack VMs on hosts based on their flavor.
func (s *VMwareHanaBinpackingStep) Run(traceLog *slog.Logger, request api.ExternalSchedulerRequest) (*lib.FilterWeigherPipelineStepResult, error) {
	result := s.IncludeAllHostsFromRequest(request)
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

	// Fetch the host capabilities.
	// Note: due to the vmware spec selector, it is expected that
	// this step is only executed for VMware hosts.
	hostCapabilitiesKnowledge := &v1alpha1.Knowledge{}
	if err := s.Client.Get(
		context.Background(),
		client.ObjectKey{Name: "host-capabilities"},
		hostCapabilitiesKnowledge,
	); err != nil {
		return nil, err
	}
	hostCapabilities, err := v1alpha1.
		UnboxFeatureList[compute.HostCapabilities](hostCapabilitiesKnowledge.Status.Raw)
	if err != nil {
		return nil, err
	}
	capabilityByHost := make(map[string]compute.HostCapabilities, len(request.Hosts))
	for _, hostCapability := range hostCapabilities {
		capabilityByHost[hostCapability.ComputeHost] = hostCapability
	}

	// Set no-effect for hosts without HANA_EXCLUSIVE trait
	hanaExclusiveHosts := make(map[string]bool)
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
		hanaExclusiveHosts[host.ComputeHost] = true
	}

	hostUtilizationKnowledge := &v1alpha1.Knowledge{}
	if err := s.Client.Get(
		context.Background(),
		client.ObjectKey{Name: "host-utilization"},
		hostUtilizationKnowledge,
	); err != nil {
		return nil, err
	}
	hostUtilizations, err := v1alpha1.
		UnboxFeatureList[compute.HostUtilization](hostUtilizationKnowledge.Status.Raw)
	if err != nil {
		return nil, err
	}
	for _, hostUtilization := range hostUtilizations {
		// Only calculate activations for HANA_EXCLUSIVE hosts
		if !hanaExclusiveHosts[hostUtilization.ComputeHost] {
			continue
		}
		// Only modify the weight if the host is in the scenario and has HANA_EXCLUSIVE trait.
		if _, ok := result.Activations[hostUtilization.ComputeHost]; !ok {
			continue
		}
		after := hostUtilization.RAMUtilizedPct +
			(float64(request.Spec.Data.Flavor.Data.MemoryMB) /
				hostUtilization.TotalRAMAllocatableMB * 100)
		result.
			Statistics["ram utilized after"].
			Subjects[hostUtilization.ComputeHost] = after

		// Only apply activation if the projected utilization is within the acceptable range
		if after < s.Options.RAMUtilizedAfterLowerBoundPct || after > s.Options.RAMUtilizedAfterUpperBoundPct {
			result.Activations[hostUtilization.ComputeHost] = s.NoEffect()
		} else {
			result.Activations[hostUtilization.ComputeHost] = lib.MinMaxScale(
				after,
				s.Options.RAMUtilizedAfterLowerBoundPct,
				s.Options.RAMUtilizedAfterUpperBoundPct,
				s.Options.RAMUtilizedAfterActivationLowerBound,
				s.Options.RAMUtilizedAfterActivationUpperBound,
			)
		}
	}

	return result, nil
}
