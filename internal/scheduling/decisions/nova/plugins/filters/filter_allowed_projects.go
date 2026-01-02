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

type FilterAllowedProjectsStep struct {
	lib.BaseStep[api.ExternalSchedulerRequest, lib.EmptyStepOpts]
}

// Lock certain hosts for certain projects, based on the hypervisor spec.
// Note that hosts without specified projects are still accessible.
func (s *FilterAllowedProjectsStep) Run(traceLog *slog.Logger, request api.ExternalSchedulerRequest) (*lib.StepResult, error) {
	result := s.PrepareResult(request)
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
			continue
		}
		if !slices.Contains(hv.Spec.AllowedProjects, request.Spec.Data.ProjectID) {
			// Project is not allowed on this hypervisor, filter it out.
			delete(result.Activations, hv.Name)
			traceLog.Info("filtering host not allowing project", "host", hv.Name)
		}
	}
	return result, nil
}
