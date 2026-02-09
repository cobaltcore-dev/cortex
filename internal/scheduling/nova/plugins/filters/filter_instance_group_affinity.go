// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package filters

import (
	"log/slog"
	"slices"

	api "github.com/cobaltcore-dev/cortex/api/delegation/nova"
	"github.com/cobaltcore-dev/cortex/internal/scheduling/lib"
)

type FilterInstanceGroupAffinityStep struct {
	lib.BaseFilter[api.ExternalSchedulerRequest, lib.EmptyFilterWeigherPipelineStepOpts]
}

// Select hosts in spec.instance_group.
func (s *FilterInstanceGroupAffinityStep) Run(
	traceLog *slog.Logger,
	request api.ExternalSchedulerRequest,
) (*lib.FilterWeigherPipelineStepResult, error) {

	result := s.IncludeAllHostsFromRequest(request)

	ig := request.Spec.Data.InstanceGroup
	if ig == nil {
		traceLog.Info("no instance group in request, skipping filter")
		return result, nil
	}
	policy := ig.Data.Policy
	if policy != "affinity" {
		traceLog.Info("instance group policy is not 'affinity', skipping filter")
		return result, nil
	}

	if len(ig.Data.Hosts) == 0 {
		// Nothing to do.
		traceLog.Info("instance group has no hosts, skipping filter")
		return result, nil
	}

	for host := range result.Activations {
		if slices.Contains(ig.Data.Hosts, host) {
			traceLog.Info("host is in instance group, keeping", "host", host)
			continue
		}
		delete(result.Activations, host)
		traceLog.Info("filtered out host not in instance group", "host", host)
	}
	return result, nil
}

func init() {
	Index["filter_instance_group_affinity"] = func() NovaFilter { return &FilterInstanceGroupAffinityStep{} }
}
