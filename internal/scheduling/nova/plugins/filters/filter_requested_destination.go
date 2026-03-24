// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package filters

import (
	"context"
	"log/slog"
	"slices"

	api "github.com/cobaltcore-dev/cortex/api/external/nova"
	"github.com/cobaltcore-dev/cortex/internal/scheduling/lib"
	hv1 "github.com/cobaltcore-dev/openstack-hypervisor-operator/api/v1"
)

type FilterRequestedDestinationStepOpts struct {
	// IgnoredAggregates specifies a list of aggregates to ignore when filtering
	// hosts based on the requested destination. This can be used to exclude
	// certain aggregates from consideration, for example AZ aggregates
	// that are already considered by the availability zone filter.
	IgnoredAggregates []string
	// IgnoredHostnames specifies a list of hostnames to ignore when filtering
	// hosts based on the requested destination. This can be used to exclude
	// certain hosts from consideration, for example if they are known to be
	// unsuitable for the workload.
	IgnoredHostnames []string
}

// Validate the options to ensure they are correct before running the weigher.
func (o FilterRequestedDestinationStepOpts) Validate() error {
	// No specific validation needed for this filter, but we could add checks here
	// if we wanted to enforce certain constraints on the options.
	return nil
}

type FilterRequestedDestinationStep struct {
	lib.BaseFilter[api.ExternalSchedulerRequest, FilterRequestedDestinationStepOpts]
}

// processRequestedAggregates filters hosts based on the requested aggregates.
// It removes hosts that are not part of any of the requested aggregates,
// respecting the IgnoredAggregates option. Returns early without filtering
// if all requested aggregates are in the ignored list.
func (s *FilterRequestedDestinationStep) processRequestedAggregates(
	traceLog *slog.Logger,
	aggregates []string,
	hvsByName map[string]hv1.Hypervisor,
	activations map[string]float64,
) {

	if len(aggregates) == 0 {
		return
	}
	aggregatesToConsider := make([]string, 0, len(aggregates))
	for _, agg := range aggregates {
		if slices.Contains(s.Options.IgnoredAggregates, agg) {
			traceLog.Info("ignoring aggregate in requested_destination as it is in the ignored list", "aggregate", agg)
			continue
		}
		aggregatesToConsider = append(aggregatesToConsider, agg)
	}
	if len(aggregatesToConsider) == 0 {
		traceLog.Info("all aggregates in requested_destination are in the ignored list, skipping aggregate filtering")
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
		found := false
		for _, reqAgg := range aggregatesToConsider {
			if slices.Contains(hvAggregateUUIDs, reqAgg) {
				found = true
				break
			}
		}
		if !found {
			delete(activations, host)
			traceLog.Info(
				"filtered out host not in requested_destination aggregates",
				"host", host, "hostAggregates", hvAggregateUUIDs,
				"requestedAggregates", aggregates,
				"ignoredAggregates", s.Options.IgnoredAggregates,
				"aggregatesConsidered", aggregatesToConsider,
			)
			continue
		}
		traceLog.Info("host is in requested_destination aggregates, keeping", "host", host)
	}
}

// processRequestedHost filters hosts based on the requested specific host.
// It removes all hosts except the one matching the requested hostname,
// respecting the IgnoredHostnames option.
func (s *FilterRequestedDestinationStep) processRequestedHost(
	traceLog *slog.Logger,
	host string,
	activations map[string]float64,
) {

	if host == "" {
		traceLog.Info("no specific host in requested_destination, skipping host filtering")
		return
	}
	if slices.Contains(s.Options.IgnoredHostnames, host) {
		traceLog.Info("requested_destination host is in the ignored hostnames list, skipping host filtering", "host", host)
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
// host filtering. This filter respects the IgnoredAggregates and IgnoredHostnames
// options to skip filtering for specific aggregates or hosts.
func (s *FilterRequestedDestinationStep) Run(traceLog *slog.Logger, request api.ExternalSchedulerRequest) (*lib.FilterWeigherPipelineStepResult, error) {
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
	if err := s.Client.List(context.Background(), hvs); err != nil {
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
