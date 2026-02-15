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

type FilterAggregateMetadata struct {
	lib.BaseFilter[api.ExternalSchedulerRequest, lib.EmptyFilterWeigherPipelineStepOpts]
}

// Restrict hosts to specific projects if they are in an aggregate that has
// the "filter_tenant_id" metadata key set.
func (s *FilterAggregateMetadata) Run(ctx context.Context, traceLog *slog.Logger, request api.ExternalSchedulerRequest) (*lib.FilterWeigherPipelineStepResult, error) {
	result := s.IncludeAllHostsFromRequest(request)

	hvs := &hv1.HypervisorList{}
	if err := s.Client.List(ctx, hvs); err != nil {
		traceLog.Error("failed to list hypervisors", "error", err)
		return nil, err
	}

	restrictedProjectsByHost := make(map[string][]string)
	for _, hv := range hvs.Items {
		for _, aggregate := range hv.Status.Aggregates {
			tenantID, ok := aggregate.Metadata["filter_tenant_id"]
			if !ok {
				traceLog.Info("aggregate does not have filter_tenant_id metadata, skipping",
					"aggregate", aggregate.Name)
				continue
			}
			restrictedProjectsByHost[hv.Name] = append(restrictedProjectsByHost[hv.Name], tenantID)
			traceLog.Info("host is in aggregate with filter_tenant_id, adding restriction",
				"host", hv.Name, "aggregate", aggregate.Name, "tenant_id", tenantID)
		}
	}

	for host, restrictedProjects := range restrictedProjectsByHost {
		if !slices.Contains(restrictedProjects, request.Spec.Data.ProjectID) {
			// Project is not allowed on this hypervisor, filter it out.
			delete(result.Activations, host)
			traceLog.Info(
				"filtering host not allowing project based on aggregate metadata",
				"host", host,
				"project", request.Spec.Data.ProjectID,
				"restricted_projects", restrictedProjects,
			)
		} else {
			traceLog.Info(
				"host allows project based on aggregate metadata, keeping",
				"host", host,
				"project", request.Spec.Data.ProjectID,
				"restricted_projects", restrictedProjects,
			)
		}
	}
	return result, nil
}

func init() {
	Index["filter_aggregate_metadata"] = func() NovaFilter { return &FilterAggregateMetadata{} }
}
