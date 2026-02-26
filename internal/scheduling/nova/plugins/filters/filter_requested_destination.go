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

// If `requested_destination` is set in the request spec, filter hosts
// accordingly. The requested destination can be a specific host, or
// an aggregate.
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

	// If aggregates are specified, only consider hosts in these aggregates.
	if len(rd.Data.Aggregates) > 0 {
		// Strip off any of the ignored aggregates from the requested aggregates.
		aggregatesToConsider := make([]string, 0, len(rd.Data.Aggregates))
		for _, agg := range rd.Data.Aggregates {
			if slices.Contains(s.Options.IgnoredAggregates, agg) {
				traceLog.Info("ignoring aggregate in requested_destination as it is in the ignored list", "aggregate", agg)
				continue
			}
			aggregatesToConsider = append(aggregatesToConsider, agg)
		}
		if len(aggregatesToConsider) == 0 {
			traceLog.Info("all aggregates in requested_destination are in the ignored list, skipping aggregate filtering")
			return result, nil
		}
		for host := range result.Activations {
			hv, exists := hvsByName[host]
			if !exists {
				delete(result.Activations, host)
				traceLog.Info("filtered out host not in requested_destination aggregates (unknown host)", "host", host)
				continue
			}
			hvAggregates := hv.Spec.Aggregates
			hvAggregates = append(hvAggregates, hv.Status.Aggregates...)
			// Check if any of the host's aggregates match the requested aggregates.
			found := false
			for _, reqAgg := range aggregatesToConsider {
				if slices.Contains(hvAggregates, reqAgg) {
					found = true
					break
				}
			}
			if !found {
				delete(result.Activations, host)
				traceLog.Info(
					"filtered out host not in requested_destination aggregates",
					"host", host, "hostAggregates", hvAggregates,
					"requestedAggregates", rd.Data.Aggregates,
					"ignoredAggregates", s.Options.IgnoredAggregates,
					"aggregatesConsidered", aggregatesToConsider,
				)
				continue
			}
			traceLog.Info("host is in requested_destination aggregates, keeping", "host", host)
		}
	}

	// If a specific host is requested, only consider that host.
	if rd.Data.Host != "" {
		if slices.Contains(s.Options.IgnoredHostnames, rd.Data.Host) {
			traceLog.Info("requested_destination host is in the ignored hostnames list, skipping host filtering", "host", rd.Data.Host)
		} else {
			for host := range result.Activations {
				if host != rd.Data.Host {
					delete(result.Activations, host)
					traceLog.Info("filtered out host not matching requested_destination host", "host", host)
					continue
				}
				traceLog.Info("host matches requested_destination host, keeping", "host", host)
			}
		}
	}

	return result, nil
}

func init() {
	Index["filter_requested_destination"] = func() NovaFilter { return &FilterRequestedDestinationStep{} }
}
