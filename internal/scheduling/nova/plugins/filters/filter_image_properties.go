// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package filters

import (
	"context"
	"log/slog"

	api "github.com/cobaltcore-dev/cortex/api/external/nova"
	"github.com/cobaltcore-dev/cortex/internal/scheduling/lib"
	hv1 "github.com/cobaltcore-dev/openstack-hypervisor-operator/api/v1"
)

type FilterImagePropertiesStep struct {
	lib.BaseFilter[api.ExternalSchedulerRequest, lib.EmptyFilterWeigherPipelineStepOpts]
}

// Filter hosts based on image properties given in the request spec.
func (s *FilterImagePropertiesStep) Run(traceLog *slog.Logger, request api.ExternalSchedulerRequest) (*lib.FilterWeigherPipelineStepResult, error) {
	result := s.IncludeAllHostsFromRequest(request)
	// If the image properties indicate any other hypervisor type than kvm,
	// we filter out all known kvm hypervisors.
	hvType, err := request.Spec.Data.Image.Data.GetHypervisorType()
	if err != nil {
		// Hypervisor type could not be determined. In this case, just
		// ignore the filter and return all hosts.
		traceLog.Warn("could not determine hypervisor type from image properties",
			"error", err)
		return result, nil
	}
	if hvType != api.NovaImageMetaHVTypeKVM {
		traceLog.Info("filtering out all known kvm hypervisors since image properties indicate a different hypervisor type",
			"image_hypervisor_type", hvType)
		hvs := new(hv1.HypervisorList)
		if err := s.Client.List(context.Background(), hvs); err != nil {
			traceLog.Error("failed to list hypervisors", "error", err)
			return nil, err
		}
		for _, hv := range hvs.Items {
			delete(result.Activations, hv.Name)
			traceLog.Info("filtering host which is kvm hypervisor", "host", hv.Name)
		}
	}
	return result, nil
}

func init() {
	Index["filter_image_properties"] = func() NovaFilter { return &FilterImagePropertiesStep{} }
}
