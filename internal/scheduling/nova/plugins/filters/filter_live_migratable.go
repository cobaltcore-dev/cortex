// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package filters

import (
	"context"
	"errors"
	"github.com/go-logr/logr"
	"slices"

	api "github.com/cobaltcore-dev/cortex/api/external/nova"
	"github.com/cobaltcore-dev/cortex/internal/scheduling/compute"
	"github.com/cobaltcore-dev/cortex/internal/scheduling/lib"
	hv1 "github.com/cobaltcore-dev/openstack-hypervisor-operator/api/v1"
)

type FilterLiveMigratableStep struct {
	lib.BaseFilter[compute.PlacementRequest, lib.EmptyFilterWeigherPipelineStepOpts]
}

// Check if a vm can be live migrated from a source to a given target host.
func (s *FilterLiveMigratableStep) checkHasSufficientFeatures(
	sourceHV hv1.Hypervisor,
	targetHV hv1.Hypervisor,
) error {

	// Needs to be the same cpu architecture
	if sourceHV.Status.Capabilities.HostCpuArch != targetHV.Status.Capabilities.HostCpuArch {
		return errors.New("cpu architectures do not match")
	}

	for _, mode := range sourceHV.Status.DomainCapabilities.SupportedCpuModes {
		if !slices.Contains(targetHV.Status.DomainCapabilities.SupportedCpuModes, mode) {
			return errors.New("cpu modes do not match")
		}
	}
	for _, feature := range sourceHV.Status.DomainCapabilities.SupportedFeatures {
		if !slices.Contains(targetHV.Status.DomainCapabilities.SupportedFeatures, feature) {
			return errors.New("hv features do not match")
		}
	}
	for _, device := range sourceHV.Status.DomainCapabilities.SupportedDevices {
		if !slices.Contains(targetHV.Status.DomainCapabilities.SupportedDevices, device) {
			return errors.New("emulated devices do not match")
		}
	}
	return nil
}

// Ensure the target host of a live migration can accept the migrating VM.
func (s *FilterLiveMigratableStep) Run(
	traceLog logr.Logger,
	request compute.PlacementRequest,
) (*lib.FilterWeigherPipelineStepResult, error) {

	result := s.IncludeAllHostsFromRequest(request)

	if request.Intent != api.LiveMigrationIntent {
		traceLog.V(1).Info("not a live migration request, skipping filter")
		return result, nil
	}

	sourceHost := request.SourceHost
	if sourceHost == "" {
		traceLog.V(1).Info("no source host set, skipping filter")
		return result, nil
	}

	hvs := &hv1.HypervisorList{}
	if err := s.Client.List(context.Background(), hvs); err != nil {
		traceLog.Error(err, "failed to list hypervisors")
		return nil, err
	}
	hvsByName := make(map[string]hv1.Hypervisor)
	for _, hv := range hvs.Items {
		hvsByName[hv.Name] = hv
	}

	sourceHV, ok := hvsByName[sourceHost]
	if !ok {
		err := errors.New("source host hypervisor not found")
		traceLog.Error(err, "failed to find source host hypervisor", "host", sourceHost)
		return nil, err
	}
	for host := range result.Activations {
		targetHV, ok := hvsByName[host]
		if !ok {
			traceLog.Error(nil, "hypervisor not found for host", "host", host)
			delete(result.Activations, host)
			continue
		}
		if err := s.checkHasSufficientFeatures(sourceHV, targetHV); err != nil {
			delete(result.Activations, host)
			traceLog.V(1).Info(
				"filtered out host not suitable for live migration",
				"host", host,
				"reason", err.Error(),
			)
			continue
		}
		traceLog.V(1).Info("host is suitable for live migration, keeping", "host", host)
	}
	return result, nil
}

func init() {
	Index["filter_live_migratable"] = func() NovaFilter { return &FilterLiveMigratableStep{} }
}
