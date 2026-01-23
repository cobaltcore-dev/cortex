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
	lib.BaseStep[api.ExternalSchedulerRequest, lib.EmptyStepOpts]
}

// Select hosts in spec.instance_group.
func (s *FilterInstanceGroupAffinityStep) Run(
	traceLog *slog.Logger,
	request api.ExternalSchedulerRequest,
) (*lib.StepResult, error) {

	result := s.IncludeAllHostsFromRequest(request)

	ig := request.Spec.Data.InstanceGroup
	if ig == nil {
		traceLog.Debug("no instance group in request, skipping filter")
		return result, nil
	}
	policy := ig.Data.Policy
	if policy != "affinity" {
		traceLog.Debug("instance group policy is not 'affinity', skipping filter")
		return result, nil
	}

	if len(ig.Data.Hosts) == 0 {
		// Nothing to do.
		traceLog.Debug("instance group has no hosts, skipping filter")
		return result, nil
	}

	for host := range result.Activations {
		if slices.Contains(ig.Data.Hosts, host) {
			continue
		}
		delete(result.Activations, host)
		traceLog.Info("filtered out host not in instance group", slog.String("host", host))
	}
	return result, nil
}
