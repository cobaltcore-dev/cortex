// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package filters

import (
	"context"
	"log/slog"

	api "github.com/cobaltcore-dev/cortex/api/external/nova"
	"github.com/cobaltcore-dev/cortex/api/v1alpha1"
	"github.com/cobaltcore-dev/cortex/internal/scheduling/lib"
)

// Step that excludes hosts a rescheduler drain is actively blocking from
// incoming placement. When a drain request with reserve is being worked on, it
// records the hosts it is draining in status.blockedHosts so that no new
// workload is placed onto a host the rescheduler is emptying (which would undo
// the drain). This filter reads that signal across all rescheduling requests and
// removes those hosts from the candidate set.
type FilterDrainBlockStep struct {
	lib.BaseFilter[api.ExternalSchedulerRequest, lib.EmptyFilterWeigherPipelineStepOpts]
}

func (s *FilterDrainBlockStep) Run(
	traceLog *slog.Logger,
	request api.ExternalSchedulerRequest,
) (*lib.FilterWeigherPipelineStepResult, error) {

	result := s.IncludeAllHostsFromRequest(request)

	requests := &v1alpha1.ReschedulingRequestList{}
	if err := s.Client.List(context.Background(), requests); err != nil {
		traceLog.Error("failed to list rescheduling requests", "error", err)
		return nil, err
	}

	for i := range requests.Items {
		for _, host := range requests.Items[i].Status.BlockedHosts {
			if _, ok := result.Activations[host]; !ok {
				continue
			}
			delete(result.Activations, host)
			traceLog.Debug(
				"filtering out host blocked by rescheduler drain",
				"host", host, "request", requests.Items[i].Name,
			)
		}
	}
	return result, nil
}

func init() {
	Index["filter_drain_block"] = func() NovaFilter { return &FilterDrainBlockStep{} }
}
