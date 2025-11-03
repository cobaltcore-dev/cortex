// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package shared

import (
	"log/slog"
	"strings"

	"github.com/cobaltcore-dev/cortex/knowledge/api/features/shared"
	api "github.com/cobaltcore-dev/cortex/scheduling/api/delegation/nova"
	"github.com/cobaltcore-dev/cortex/scheduling/internal/lib"
)

type FilterCorrectAZStep struct {
	lib.BaseStep[api.ExternalSchedulerRequest, lib.EmptyStepOpts]
}

// Only get hosts in the requested az.
func (s *FilterCorrectAZStep) Run(traceLog *slog.Logger, request api.ExternalSchedulerRequest) (*lib.StepResult, error) {
	result := s.PrepareResult(request)
	if request.Spec.Data.AvailabilityZone == "" {
		traceLog.Debug("no availability zone requested, skipping filter_correct_az step")
		return result, nil
	}
	var computeHostsInAZ []string
	if _, err := s.DB.SelectTimed("scheduler-nova", &computeHostsInAZ, `
        SELECT DISTINCT compute_host
        FROM `+shared.HostAZ{}.TableName()+`
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
		traceLog.Debug("filtering host outside requested az", "host", host)
	}
	return result, nil
}
