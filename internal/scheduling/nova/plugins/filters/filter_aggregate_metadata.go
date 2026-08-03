// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package filters

import (
	"context"
	"log/slog"
	"slices"
	"strings"

	api "github.com/cobaltcore-dev/cortex/api/external/nova"
	"github.com/cobaltcore-dev/cortex/api/v1alpha1"
	"github.com/cobaltcore-dev/cortex/internal/scheduling/lib"
	hv1 "github.com/cobaltcore-dev/openstack-hypervisor-operator/api/v1"
)

type FilterAggregateMetadata struct {
	lib.BaseFilter[api.ExternalSchedulerRequest, lib.EmptyFilterWeigherPipelineStepOpts]
}

// Restrict hosts to specific projects if they are in an aggregate that has
// the "filter_tenant_id" metadata key set.
func (s *FilterAggregateMetadata) Run(traceLog *slog.Logger, request api.ExternalSchedulerRequest) (*lib.FilterWeigherPipelineStepResult, error) {
	result := s.IncludeAllHostsFromRequest(request)
	// Failover and capacity probe calls are not placed on behalf of a tenant project;
	if intent, err := request.GetIntent(); err == nil && slices.Contains([]v1alpha1.SchedulingIntent{
		api.ReserveForFailoverIntent,
		api.ReuseFailoverReservationIntent,
		api.CapacityProbeIntent,
	}, intent) {
		return result, nil
	}

	hvs := &hv1.HypervisorList{}
	if err := s.Client.List(context.Background(), hvs); err != nil {
		traceLog.Error("failed to list hypervisors", "error", err)
		return nil, err
	}

	restrictedProjectsByHost := make(map[string][]string)
	for _, hv := range hvs.Items {
		for _, aggregate := range hv.Status.Aggregates {
			// Any metadata key prefixed with "filter_tenant_id" restricts the
			// aggregate to the referenced projects. Multiple numbered keys (e.g.
			// filter_tenant_id, filter_tenant_id1, ...) circumvent per-field
			// database string length limits; each of their values can itself be
			// a comma-separated list of project ids.
			foundFilter := false
			for key, value := range aggregate.Metadata {
				if !strings.HasPrefix(key, "filter_tenant_id") {
					continue
				}
				foundFilter = true
				for projectID := range strings.SplitSeq(value, ",") {
					projectID = strings.TrimSpace(projectID)
					if projectID == "" {
						continue
					}
					restrictedProjectsByHost[hv.Name] = append(restrictedProjectsByHost[hv.Name], projectID)
				}
				traceLog.Info("host is in aggregate with filter_tenant_id, adding restriction",
					"host", hv.Name, "aggregate", aggregate.Name, "key", key, "tenant_id", value)
			}
			if !foundFilter {
				traceLog.Info("aggregate does not have filter_tenant_id metadata, skipping",
					"aggregate", aggregate.Name)
			}
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
