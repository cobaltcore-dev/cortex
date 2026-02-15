// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package filters

import (
	"context"
	"log/slog"
	"slices"

	api "github.com/cobaltcore-dev/cortex/api/external/nova"
	"github.com/cobaltcore-dev/cortex/internal/scheduling/lib"
	hv1 "github.com/cobaltcore-dev/openstack-hypervisor-operator/api/v1"
)

type FilterPackedVirtqueueStep struct {
	lib.BaseFilter[api.ExternalSchedulerRequest, lib.EmptyFilterWeigherPipelineStepOpts]
}

// If requested, only get hosts with packed virtqueues.
func (s *FilterPackedVirtqueueStep) Run(ctx context.Context, traceLog *slog.Logger, request api.ExternalSchedulerRequest) (*lib.FilterWeigherPipelineStepResult, error) {
	result := s.IncludeAllHostsFromRequest(request)
	// We don't care about the value.
	_, reqInSpecs := request.Spec.Data.Flavor.Data.ExtraSpecs["hw:virtio_packed_ring"]
	_, reqInProps := request.Spec.Data.Image.Data.Properties.Data["hw_virtio_packed_ring"]
	if !reqInSpecs && !reqInProps {
		traceLog.Info("no request for packed virtqueues, skipping filter")
		return result, nil // No packed virtqueue requested, nothing to filter.
	}

	hvs := &hv1.HypervisorList{}
	if err := s.Client.List(ctx, hvs); err != nil {
		traceLog.Error("failed to list hypervisors", "error", err)
		return nil, err
	}
	hvsWithTrait := make(map[string]struct{})
	for _, hv := range hvs.Items {
		traits := hv.Status.Traits
		traits = append(traits, hv.Spec.CustomTraits...)
		if !slices.Contains(traits, "COMPUTE_NET_VIRTIO_PACKED") {
			continue
		}
		hvsWithTrait[hv.Name] = struct{}{}
	}

	traceLog.Info("hosts with packed virtqueues", "hosts", hvsWithTrait)
	for host := range result.Activations {
		if _, ok := hvsWithTrait[host]; ok {
			traceLog.Info("host has packed virtqueues, keeping", "host", host)
			continue
		}
		delete(result.Activations, host)
		traceLog.Info("filtering host without packed virtqueues", "host", host)
	}
	return result, nil
}

func init() {
	Index["filter_packed_virtqueue"] = func() NovaFilter { return &FilterPackedVirtqueueStep{} }
}
