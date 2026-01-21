// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package filters

import (
	"context"
	"log/slog"
	"slices"

	api "github.com/cobaltcore-dev/cortex/api/delegation/nova"
	"github.com/cobaltcore-dev/cortex/internal/scheduling/lib"
	hv1 "github.com/cobaltcore-dev/openstack-hypervisor-operator/api/v1"
)

type FilterPackedVirtqueueStep struct {
	lib.Filter[api.ExternalSchedulerRequest, lib.EmptyStepOpts]
}

// If requested, only get hosts with packed virtqueues.
func (s *FilterPackedVirtqueueStep) Run(traceLog *slog.Logger, request api.ExternalSchedulerRequest) (*lib.StepResult, error) {
	result := s.PrepareResult(request)
	// We don't care about the value.
	_, reqInSpecs := request.Spec.Data.Flavor.Data.ExtraSpecs["hw:virtio_packed_ring"]
	_, reqInProps := request.Spec.Data.Image.Data.Properties.Data["hw_virtio_packed_ring"]
	if !reqInSpecs && !reqInProps {
		return result, nil // No packed virtqueue requested, nothing to filter.
	}

	hvs := &hv1.HypervisorList{}
	if err := s.Client.List(context.Background(), hvs); err != nil {
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
			continue
		}
		delete(result.Activations, host)
		traceLog.Info("filtering host without packed virtqueues", "host", host)
	}
	return result, nil
}
