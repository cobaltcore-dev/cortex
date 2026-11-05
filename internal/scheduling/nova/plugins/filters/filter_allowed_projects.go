// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package filters

import (
	"context"
	"github.com/go-logr/logr"
	"slices"

	api "github.com/cobaltcore-dev/cortex/api/external/nova"
	"github.com/cobaltcore-dev/cortex/internal/scheduling/compute"
	"github.com/cobaltcore-dev/cortex/internal/scheduling/lib"
	hv1 "github.com/cobaltcore-dev/openstack-hypervisor-operator/api/v1"
)

type FilterAllowedProjectsStep struct {
	lib.BaseFilter[compute.PlacementRequest, lib.EmptyFilterWeigherPipelineStepOpts]
}

// Lock certain hosts for certain projects, based on the hypervisor spec.
// Note that hosts without specified projects are still accessible.
func (s *FilterAllowedProjectsStep) Run(traceLog logr.Logger, request compute.PlacementRequest) (*lib.FilterWeigherPipelineStepResult, error) {
	result := s.IncludeAllHostsFromRequest(request)
	// Capacity probe calls are synthetic and not placed on behalf of a tenant
	// project, so they must ignore tenant pinning. Failover reservations DO respect
	// tenant pinning: a reservation on a host the VM's project cannot use is dead
	// weight and would be deleted at validation time.
	if request.Intent == api.CapacityProbeIntent {
		return result, nil
	}
	if request.ProjectID == "" {
		traceLog.V(1).Info("no project ID in request, skipping filter")
		return result, nil
	}

	hvs := &hv1.HypervisorList{}
	if err := s.Client.List(context.Background(), hvs); err != nil {
		traceLog.Error(err, "failed to list hypervisors")
		return nil, err
	}

	for _, hv := range hvs.Items {
		if _, ok := result.Activations[hv.Name]; !ok {
			continue
		}
		if len(hv.Spec.AllowedProjects) == 0 {
			// Hypervisor is available for all projects.
			traceLog.V(1).Info("host allows all projects, keeping", "host", hv.Name)
			continue
		}
		if !slices.Contains(hv.Spec.AllowedProjects, request.ProjectID) {
			// Project is not allowed on this hypervisor, filter it out.
			delete(result.Activations, hv.Name)
			traceLog.V(1).Info(
				"filtering host not allowing project",
				"host", hv.Name,
				"project", request.ProjectID,
			)
		}
	}
	return result, nil
}

func init() {
	Index["filter_allowed_projects"] = func() NovaFilter { return &FilterAllowedProjectsStep{} }
}
