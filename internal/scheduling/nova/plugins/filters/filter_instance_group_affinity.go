// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package filters

import (
	"github.com/go-logr/logr"
	"slices"

	api "github.com/cobaltcore-dev/cortex/api/external/nova"
	"github.com/cobaltcore-dev/cortex/api/v1alpha1"
	"github.com/cobaltcore-dev/cortex/internal/scheduling/compute"
	"github.com/cobaltcore-dev/cortex/internal/scheduling/lib"
)

type FilterInstanceGroupAffinityStep struct {
	lib.BaseFilter[compute.PlacementRequest, lib.EmptyFilterWeigherPipelineStepOpts]
}

// Select hosts in spec.instance_group.
func (s *FilterInstanceGroupAffinityStep) Run(
	traceLog logr.Logger,
	request compute.PlacementRequest,
) (*lib.FilterWeigherPipelineStepResult, error) {

	result := s.IncludeAllHostsFromRequest(request)
	// Instance group hints are only present in user-initiated VM placement requests;
	if slices.Contains([]v1alpha1.SchedulingIntent{
		api.ReserveForFailoverIntent,
		api.ReuseFailoverReservationIntent,
		api.ReserveForCommittedResourceIntent,
		api.CapacityProbeIntent,
	}, request.Intent) {
		return result, nil
	}

	ig := request.InstanceGroup
	if ig == nil {
		traceLog.V(1).Info("no instance group in request, skipping filter")
		return result, nil
	}
	policy := ig.Policy
	if policy != "affinity" {
		traceLog.V(1).Info("instance group policy is not 'affinity', skipping filter")
		return result, nil
	}

	if len(ig.Hosts) == 0 {
		// Nothing to do.
		traceLog.V(1).Info("instance group has no hosts, skipping filter")
		return result, nil
	}

	for host := range result.Activations {
		if slices.Contains(ig.Hosts, host) {
			traceLog.V(1).Info("host is in instance group, keeping", "host", host)
			continue
		}
		delete(result.Activations, host)
		traceLog.V(1).Info("filtered out host not in instance group", "host", host)
	}
	return result, nil
}

func init() {
	Index["filter_instance_group_affinity"] = func() NovaFilter { return &FilterInstanceGroupAffinityStep{} }
}
