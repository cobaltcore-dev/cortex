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

type FilterHasAcceleratorsStep struct {
	lib.BaseFilter[api.ExternalSchedulerRequest, lib.EmptyStepOpts]
}

// If requested, only get hosts with accelerators.
func (s *FilterHasAcceleratorsStep) Run(traceLog *slog.Logger, request api.ExternalSchedulerRequest) (*lib.StepResult, error) {
	result := s.PrepareResult(request)
	extraSpecs := request.Spec.Data.Flavor.Data.ExtraSpecs
	if _, ok := extraSpecs["accel:device_profile"]; !ok {
		traceLog.Debug("no accelerators requested")
		return result, nil
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
		if !slices.Contains(traits, "COMPUTE_ACCELERATORS") {
			continue
		}
		hvsWithTrait[hv.Name] = struct{}{}
	}

	traceLog.Info("hosts with accelerators", "hosts", hvsWithTrait)
	for host := range result.Activations {
		if _, ok := hvsWithTrait[host]; ok {
			continue
		}
		delete(result.Activations, host)
		traceLog.Info("filtering host without accelerators", "host", host)
	}
	return result, nil
}
