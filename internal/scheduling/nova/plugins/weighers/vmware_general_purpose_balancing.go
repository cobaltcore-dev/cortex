// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package weighers

import (
	"context"
	"errors"
	"log/slog"
	"strings"

	api "github.com/cobaltcore-dev/cortex/api/external/nova"
	"github.com/cobaltcore-dev/cortex/api/v1alpha1"
	"github.com/cobaltcore-dev/cortex/internal/knowledge/extractor/plugins/compute"
	"github.com/cobaltcore-dev/cortex/internal/scheduling/lib"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// Options for the scheduling step, given through the step config in the service yaml file.
type VMwareGeneralPurposeBalancingStepOpts struct {
	RAMUtilizedLowerBoundPct        float64 `json:"ramUtilizedLowerBoundPct"` // -> mapped to ActivationLowerBound
	RAMUtilizedUpperBoundPct        float64 `json:"ramUtilizedUpperBoundPct"` // -> mapped to ActivationUpperBound
	RAMUtilizedActivationLowerBound float64 `json:"ramUtilizedActivationLowerBound"`
	RAMUtilizedActivationUpperBound float64 `json:"ramUtilizedActivationUpperBound"`
}

func (o VMwareGeneralPurposeBalancingStepOpts) Validate() error {
	// Avoid zero-division during min-max scaling.
	if o.RAMUtilizedLowerBoundPct == o.RAMUtilizedUpperBoundPct {
		return errors.New("ramUtilizedLowerBound and ramUtilizedUpperBound must not be equal")
	}
	return nil
}

// Step to balance VMs on hosts based on the host's available resources.
type VMwareGeneralPurposeBalancingStep struct {
	// BaseStep is a helper struct that provides common functionality for all steps.
	lib.BaseWeigher[api.ExternalSchedulerRequest, VMwareGeneralPurposeBalancingStepOpts]
}

// Initialize the step and validate that all required knowledges are ready.
func (s *VMwareGeneralPurposeBalancingStep) Init(ctx context.Context, client client.Client, weigher v1alpha1.WeigherSpec) error {
	if err := s.BaseWeigher.Init(ctx, client, weigher); err != nil {
		return err
	}
	if err := s.CheckKnowledges(ctx,
		types.NamespacedName{Name: "host-utilization"},
		types.NamespacedName{Name: "host-capabilities"},
	); err != nil {
		return err
	}
	return nil
}

// Pack VMs on hosts based on their flavor.
func (s *VMwareGeneralPurposeBalancingStep) Run(ctx context.Context, traceLog *slog.Logger, request api.ExternalSchedulerRequest) (*lib.FilterWeigherPipelineStepResult, error) {
	result := s.IncludeAllHostsFromRequest(request)
	// Don't execute the step for non-hana flavors.
	if strings.Contains(request.Spec.Data.Flavor.Data.Name, "hana") {
		slog.Debug("Skipping general purpose balancing step for HANA flavor", "flavor", request.Spec.Data.Flavor.Data.Name)
		return result, nil
	}

	result.Statistics["ram utilized"] = s.PrepareStats(request, "%")

	hostUtilizationKnowledge := &v1alpha1.Knowledge{}
	if err := s.Client.Get(
		ctx,
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
		// Only modify the weight if the host is in the scenario.
		if _, ok := result.Activations[hostUtilization.ComputeHost]; !ok {
			continue
		}
		result.
			Statistics["ram utilized"].
			Hosts[hostUtilization.ComputeHost] = hostUtilization.RAMUtilizedPct
		result.Activations[hostUtilization.ComputeHost] = lib.MinMaxScale(
			hostUtilization.RAMUtilizedPct,
			s.Options.RAMUtilizedLowerBoundPct,
			s.Options.RAMUtilizedUpperBoundPct,
			s.Options.RAMUtilizedActivationLowerBound,
			s.Options.RAMUtilizedActivationUpperBound,
		)
	}

	// Fetch the host capabilities.
	// Note: due to the vmware spec selector, it is expected that
	// this step is only executed for VMware hosts.
	hostCapabilitiesKnowledge := &v1alpha1.Knowledge{}
	if err := s.Client.Get(
		ctx,
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
	for _, host := range request.Hosts {
		capability, ok := capabilityByHost[host.ComputeHost]
		if !ok {
			slog.Warn("No host capabilities found for host", "host", host.ComputeHost)
			result.Activations[host.ComputeHost] = s.NoEffect()
			continue
		}
		if strings.Contains(capability.Traits, "HANA_EXCLUSIVE") {
			slog.Debug("Skipping general purpose balancing for host with HANA_EXCLUSIVE trait", "host", host.ComputeHost)
			result.Activations[host.ComputeHost] = s.NoEffect()
			continue
		}
	}

	return result, nil
}

func init() {
	Index["vmware_general_purpose_balancing"] = func() lib.Weigher[api.ExternalSchedulerRequest] {
		return &VMwareGeneralPurposeBalancingStep{}
	}
}
