// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package filters

import (
	"context"
	"github.com/go-logr/logr"
	"slices"

	api "github.com/cobaltcore-dev/cortex/api/external/nova"
	"github.com/cobaltcore-dev/cortex/api/v1alpha1"
	"github.com/cobaltcore-dev/cortex/internal/scheduling/compute"
	"github.com/cobaltcore-dev/cortex/internal/scheduling/lib"
	hv1 "github.com/cobaltcore-dev/openstack-hypervisor-operator/api/v1"
)

type FilterImagePropertiesStep struct {
	lib.BaseFilter[compute.SchedulerRequest, lib.EmptyFilterWeigherPipelineStepOpts]
}

// Filter hosts based on image properties given in the request spec.
func (s *FilterImagePropertiesStep) Run(traceLog logr.Logger, request compute.SchedulerRequest) (*lib.FilterWeigherPipelineStepResult, error) {
	result := s.IncludeAllHostsFromRequest(request)

	// Apply this filter to all requests, unless we know from the request's
	// intent that image metadata is expected to not be set.
	intentsExpectedToNotHaveImageMeta := []v1alpha1.SchedulingIntent{
		// Cortex-internal intents in which scheduling requests are sent
		// mainly based on flavor-related metadata, independent of the
		// image that'll land there.
		api.ReserveForFailoverIntent,
		api.ReuseFailoverReservationIntent,
		api.ReserveForCommittedResourceIntent,
		api.CapacityProbeIntent,
	}
	if slices.Contains(intentsExpectedToNotHaveImageMeta, request.Intent) {
		traceLog.V(1).Info("skipping filter: expected to have no image metadata",
			"intent", request.Intent)
		return result, nil
	}

	// If the image properties indicate any other hypervisor type than kvm,
	// we filter out all known kvm hypervisors. An unset image hypervisor type
	// means the image declared none, so no image constraint applies.
	hvType := request.ImageHypervisorType
	if hvType != compute.ImageHypervisorTypeUnset && hvType != compute.ImageHypervisorTypeKVM {
		traceLog.V(1).Info("filtering out all known kvm hypervisors since image properties indicate a different hypervisor type",
			"image_hypervisor_type", hvType)
		hvs := new(hv1.HypervisorList)
		if err := s.Client.List(context.Background(), hvs); err != nil {
			traceLog.Error(err, "failed to list hypervisors")
			return nil, err
		}
		for _, hv := range hvs.Items {
			delete(result.Activations, hv.Name)
			traceLog.V(1).Info("filtering host which is kvm hypervisor", "host", hv.Name)
		}
	}

	return result, nil
}

func init() {
	Index["filter_image_properties"] = func() NovaFilter { return &FilterImagePropertiesStep{} }
}
