// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package shared

import (
	"log/slog"
	"strings"

	"github.com/cobaltcore-dev/cortex/knowledge/api/datasources/openstack/nova"
	"github.com/cobaltcore-dev/cortex/knowledge/api/datasources/openstack/placement"

	api "github.com/cobaltcore-dev/cortex/scheduling/api/delegation/nova"
	"github.com/cobaltcore-dev/cortex/scheduling/internal/lib"
)

type FilterDisabledStep struct {
	lib.BaseStep[api.ExternalSchedulerRequest, lib.EmptyStepOpts]
}

// Get the name of this step, used for identification in config, logs, metrics, etc.
func (s *FilterDisabledStep) GetName() string { return "filter_disabled" }

// Only get hosts that are not disabled or down.
func (s *FilterDisabledStep) Run(traceLog *slog.Logger, request api.ExternalSchedulerRequest) (*lib.StepResult, error) {
	result := s.PrepareResult(request)
	var computeHostsActive []string
	if _, err := s.DB.SelectTimed("scheduler-nova", &computeHostsActive, `
	    SELECT h.service_host
		FROM `+placement.Trait{}.TableName()+` rpt
		JOIN `+nova.Hypervisor{}.TableName()+` h
		ON h.id = rpt.resource_provider_uuid
		WHERE
			name != 'COMPUTE_STATUS_DISABLED' AND
			h.status != 'disabled' AND
			h.state != 'down'`,
		map[string]any{"az": request.Spec.Data.AvailabilityZone},
	); err != nil {
		return nil, err
	}
	lookupStr := strings.Join(computeHostsActive, ",")
	for host := range result.Activations {
		if strings.Contains(lookupStr, host) {
			continue
		}
		delete(result.Activations, host)
		traceLog.Debug("filtering disabled/down host", "host", host)
	}
	return result, nil
}
