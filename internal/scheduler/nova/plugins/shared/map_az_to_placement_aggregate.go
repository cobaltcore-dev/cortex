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

type MapAZToPlacementAggregateStep struct {
	scheduler.BaseStep[api.ExternalSchedulerRequest, scheduler.EmptyStepOpts]
}

// Get the name of this step, used for identification in config, logs, metrics, etc.
func (s *MapAZToPlacementAggregateStep) GetName() string {
	return "map_az_to_placement_aggregate"
}

// Only get hosts in the requested az.
func (s *MapAZToPlacementAggregateStep) Run(traceLog *slog.Logger, request api.ExternalSchedulerRequest) (*scheduler.StepResult, error) {
	result := s.PrepareResult(request)
	var computeHostsInAZ []string
	if _, err := s.DB.SelectTimed("scheduler-nova", &computeHostsInAZ, `
        SELECT DISTINCT compute_host
        FROM `+nova.Aggregate{}.TableName()+`
        WHERE availability_zone = :az`,
		map[string]any{"az": request.Spec.Data.AvailabilityZone},
	); err != nil {
		return nil, err
	}
	lookupStr := strings.Join(computeHostsInAZ, ",")
	for host := range result.Activations {
		if strings.Contains(lookupStr, host) {
			continue
		}
		delete(result.Activations, host)
	}
	return result, nil
}
