// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package filters

import (
	"log/slog"
	"strings"

	api "github.com/cobaltcore-dev/cortex/api/delegation/nova"
	"github.com/cobaltcore-dev/cortex/internal/knowledge/extractor/plugins/shared"
	"github.com/cobaltcore-dev/cortex/internal/scheduling/lib"
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
	var computeHostsMatchingProject []string
	if _, err := s.DB.SelectTimed("scheduler-nova", &computeHostsMatchingProject, `
        SELECT DISTINCT compute_host
        FROM `+shared.HostPinnedProjects{}.TableName()+`
        WHERE (compute_host IS NOT NULL AND project_id = :projectID) OR (compute_host IS NOT NULL AND project_id IS NULL)`,
		map[string]any{"projectID": request.Spec.Data.ProjectID},
	); err != nil {
		return nil, err
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
