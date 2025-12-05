// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package filters

import (
	"context"
	"log/slog"
	"strings"

	api "github.com/cobaltcore-dev/cortex/api/delegation/nova"
	"github.com/cobaltcore-dev/cortex/api/v1alpha1"
	"github.com/cobaltcore-dev/cortex/internal/knowledge/extractor/plugins/shared"
	"github.com/cobaltcore-dev/cortex/internal/scheduling/lib"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

type FilterProjectAggregatesStep struct {
	lib.BaseStep[api.ExternalSchedulerRequest, lib.EmptyStepOpts]
}

// Lock certain hosts for certain projects, based on the aggregate metadata.
// Note that hosts without aggregate tenant filter are still accessible.
func (s *FilterProjectAggregatesStep) Run(traceLog *slog.Logger, request api.ExternalSchedulerRequest) (*lib.StepResult, error) {
	result := s.PrepareResult(request)
	if request.Spec.Data.ProjectID == "" {
		traceLog.Debug("no project ID in request, skipping filter")
		return result, nil
	}
	knowledge := &v1alpha1.Knowledge{}
	if err := s.Client.Get(
		context.Background(),
		client.ObjectKey{Name: "host-pinned-projects"},
		knowledge,
	); err != nil {
		return nil, err
	}
	hostPinnedProjects, err := v1alpha1.
		UnboxFeatureList[shared.HostPinnedProjects](knowledge.Status.Raw)
	if err != nil {
		return nil, err
	}
	var computeHostsMatchingProject []string
	for _, hostProj := range hostPinnedProjects {
		if hostProj.ComputeHost == nil {
			traceLog.Warn("host pinned projects knowledge has nil compute host", "entry", hostProj)
			continue
		}
		if hostProj.ProjectID == nil {
			// Host is available for all projects.
			computeHostsMatchingProject = append(computeHostsMatchingProject, *hostProj.ComputeHost)
			continue
		}
		if *hostProj.ProjectID == request.Spec.Data.ProjectID {
			computeHostsMatchingProject = append(computeHostsMatchingProject, *hostProj.ComputeHost)
		}
	}
	lookupStr := strings.Join(computeHostsMatchingProject, ",")
	for host := range result.Activations {
		if strings.Contains(lookupStr, host) {
			continue
		}
		delete(result.Activations, host)
		traceLog.Debug("filtering host not matching project aggregates", "host", host)
	}
	return result, nil
}
