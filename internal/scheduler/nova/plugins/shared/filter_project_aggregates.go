// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package shared

import (
	"log/slog"
	"strings"

	"github.com/cobaltcore-dev/cortex/internal/scheduler"
	"github.com/cobaltcore-dev/cortex/internal/scheduler/nova/api"
	"github.com/cobaltcore-dev/cortex/internal/sync/openstack/nova"
)

type FilterProjectAggregatesStep struct {
	scheduler.BaseStep[api.ExternalSchedulerRequest, scheduler.EmptyStepOpts]
}

// Get the name of this step, used for identification in config, logs, metrics, etc.
func (s *FilterProjectAggregatesStep) GetName() string { return "filter_project_aggregates" }

// Lock certain hosts for certain projects, based on the aggregate metadata.
// Note that hosts without aggregate tenant filter are still accessible.
func (s *FilterProjectAggregatesStep) Run(traceLog *slog.Logger, request api.ExternalSchedulerRequest) (*scheduler.StepResult, error) {
	result := s.PrepareResult(request)
	if request.Spec.Data.ProjectID == "" {
		traceLog.Debug("no project ID in request, skipping filter")
		return result, nil
	}
	var computeHostsMatchingProject []string
	if _, err := s.DB.SelectTimed("scheduler-nova", &computeHostsMatchingProject, `
        SELECT DISTINCT compute_host
        FROM `+nova.Aggregate{}.TableName()+`
        WHERE compute_host IS NOT NULL AND (
            metadata NOT LIKE '%filter_tenant_id%' OR
            (
                metadata LIKE '%filter_tenant_id%' AND
                metadata LIKE :projectID
            )
        )`,
		map[string]any{"projectID": "%" + request.Spec.Data.ProjectID + "%"},
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
