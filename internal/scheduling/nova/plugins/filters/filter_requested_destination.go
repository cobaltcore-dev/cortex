// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package filters

import (
	"context"
	"log/slog"
	"slices"
	"strings"

	api "github.com/cobaltcore-dev/cortex/api/external/nova"
	"github.com/cobaltcore-dev/cortex/internal/scheduling/lib"
	hv1 "github.com/cobaltcore-dev/openstack-hypervisor-operator/api/v1"
)

type FilterRequestedDestinationStep struct {
	lib.BaseFilter[api.ExternalSchedulerRequest, lib.EmptyFilterWeigherPipelineStepOpts]
}

// processRequestedAggregates filters hosts based on the requested aggregates.
// The aggregates list uses AND logic between elements, meaning a host must match
// ALL elements to pass. Each element can contain comma-separated UUIDs which use
// OR logic, meaning the host only needs to match ONE of the UUIDs in that group.
// Example: ["agg1", "agg2,agg3"] means host must be in agg1 AND (agg2 OR agg3).
func (s *FilterRequestedDestinationStep) processRequestedAggregates(
	traceLog *slog.Logger,
	aggregates []string,
	hvsByName map[string]hv1.Hypervisor,
	activations map[string]float64,
) {

	if len(aggregates) == 0 {
		return
	}
	for host := range activations {
		hv, exists := hvsByName[host]
		if !exists {
			delete(activations, host)
			traceLog.Info("filtered out host not in requested_destination aggregates (unknown host)", "host", host)
			continue
		}
		hvAggregateUUIDs := make([]string, 0, len(hv.Status.Aggregates))
		for _, agg := range hv.Status.Aggregates {
			hvAggregateUUIDs = append(hvAggregateUUIDs, agg.UUID)
		}
		// All outer elements must match (AND logic)
		// Each element can be comma-separated UUIDs (OR logic within the group)
		allMatch := true
		for _, reqAggGroup := range aggregates {
			reqAggs := strings.Split(reqAggGroup, ",")
			groupMatch := false
			for _, reqAgg := range reqAggs {
				if slices.Contains(hvAggregateUUIDs, reqAgg) {
					groupMatch = true
					break
				}
			}
			if !groupMatch {
				allMatch = false
				break
			}
		}
		if !allMatch {
			delete(activations, host)
			traceLog.Info(
				"filtered out host not in requested_destination aggregates",
				"host", host, "hostAggregates", hvAggregateUUIDs,
				"requestedAggregates", aggregates,
			)
			continue
		}
		traceLog.Info("host is in requested_destination aggregates, keeping", "host", host)
	}
}

// processRequestedHost filters hosts based on the requested specific host.
// It removes all hosts except the one matching the requested hostname.
func (s *FilterRequestedDestinationStep) processRequestedHost(
	traceLog *slog.Logger,
	host string,
	activations map[string]float64,
) {

	if host == "" {
		traceLog.Info("no specific host in requested_destination, skipping host filtering")
		return
	}
	for h := range activations {
		if h != host {
			delete(activations, h)
			traceLog.Info("filtered out host not matching requested_destination host", "host", h)
			continue
		}
		traceLog.Info("host matches requested_destination host, keeping", "host", h)
	}
}

// Run filters hosts based on the requested destination specified in the request.
// The requested destination can include a specific host, aggregates, or both.
// When both are specified, aggregate filtering is applied first, followed by
// host filtering.
func (s *FilterRequestedDestinationStep) Run(ctx context.Context, traceLog *slog.Logger, request api.ExternalSchedulerRequest) (*lib.FilterWeigherPipelineStepResult, error) {
	result := s.IncludeAllHostsFromRequest(request)
	rd := request.Spec.Data.RequestedDestination
	if rd == nil {
		traceLog.Info("no requested_destination in request, skipping filter")
		return result, nil
	}
	if len(rd.Data.Aggregates) == 0 && rd.Data.Host == "" {
		traceLog.Info("requested_destination has no host or aggregates, skipping filter")
		return result, nil
	}
	hvs := &hv1.HypervisorList{}
	if err := s.Client.List(ctx, hvs); err != nil {
		traceLog.Error("failed to list hypervisors", "error", err)
		return nil, err
	}
	hvsByName := make(map[string]hv1.Hypervisor)
	for _, hv := range hvs.Items {
		hvsByName[hv.Name] = hv
	}
	s.processRequestedAggregates(traceLog, rd.Data.Aggregates, hvsByName, result.Activations)
	s.processRequestedHost(traceLog, rd.Data.Host, result.Activations)
	return result, nil
}

func init() {
	Index["filter_requested_destination"] = func() NovaFilter { return &FilterRequestedDestinationStep{} }
}
