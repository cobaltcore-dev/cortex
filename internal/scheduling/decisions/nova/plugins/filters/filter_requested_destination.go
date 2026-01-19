// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package filters

import (
	"context"
	"log/slog"
	"slices"

	api "github.com/cobaltcore-dev/cortex/api/delegation/nova"
	"github.com/cobaltcore-dev/cortex/internal/scheduling/lib"
	hv1 "github.com/cobaltcore-dev/openstack-hypervisor-operator/api/v1"
)

type FilterRequestedDestinationStep struct {
	lib.BaseStep[api.ExternalSchedulerRequest, lib.EmptyStepOpts]
}

// If `requested_destination` is set in the request spec, filter hosts
// accordingly. The requested destination can be a specific host, or
// an aggregate.
func (s *FilterRequestedDestinationStep) Run(
	traceLog *slog.Logger,
	request api.ExternalSchedulerRequest,
) (*lib.StepResult, error) {

	result := s.PrepareResult(request)

	rd := request.Spec.Data.RequestedDestination
	if rd == nil {
		traceLog.Debug("no requested_destination in request, skipping filter")
		return result, nil
	}
	if len(rd.Data.Aggregates) == 0 && rd.Data.Host == "" {
		traceLog.Debug("requested_destination has no host or aggregates, skipping filter")
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
			for _, reqAgg := range rd.Data.Aggregates {
				if slices.Contains(hvAggregates, reqAgg) {
					found = true
					break
				}
			}
			if !found {
				delete(result.Activations, host)
				traceLog.Info("filtered out host not in requested_destination aggregates", "host", host)
			}
		}
	}

	// If a specific host is requested, only consider that host.
	if rd.Data.Host != "" {
		for host := range result.Activations {
			if host != rd.Data.Host {
				delete(result.Activations, host)
				traceLog.Info("filtered out host not matching requested_destination host", "host", host)
			}
		}
	}

	return result, nil
}
