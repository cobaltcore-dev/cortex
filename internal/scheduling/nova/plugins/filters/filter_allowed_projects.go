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

type FilterAllowedProjectsStep struct {
	lib.BaseFilter[api.ExternalSchedulerRequest, lib.EmptyFilterWeigherPipelineStepOpts]
}

// Lock certain hosts for certain projects, based on the hypervisor spec.
// Note that hosts without specified projects are still accessible.
func (s *FilterAllowedProjectsStep) Run(traceLog *slog.Logger, request api.ExternalSchedulerRequest) (*lib.FilterWeigherPipelineStepResult, error) {
	result := s.IncludeAllHostsFromRequest(request)
	if request.Spec.Data.ProjectID == "" {
		traceLog.Info("no project ID in request, skipping filter")
		return result, nil
	}

	hvs := &hv1.HypervisorList{}
	if err := s.Client.List(context.Background(), hvs); err != nil {
		traceLog.Error("failed to list hypervisors", "error", err)
		return nil, err
	}

	for _, hv := range hvs.Items {
		if len(hv.Spec.AllowedProjects) == 0 {
			// Hypervisor is available for all projects.
			traceLog.Info("host allows all projects, keeping", "host", hv.Name)
			continue
		}
		if !slices.Contains(hv.Spec.AllowedProjects, request.Spec.Data.ProjectID) {
			// Project is not allowed on this hypervisor, filter it out.
			delete(result.Activations, hv.Name)
			traceLog.Info(
				"filtering host not allowing project",
				"host", hv.Name,
				"project", request.Spec.Data.ProjectID,
			)
		}
	}
	return result, nil
}

func init() {
	Index["filter_allowed_projects"] = func() NovaFilter { return &FilterAllowedProjectsStep{} }
}
