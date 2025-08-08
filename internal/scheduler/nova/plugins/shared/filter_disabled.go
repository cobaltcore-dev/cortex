// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package shared

import (
	"log/slog"
	"strings"

	"github.com/cobaltcore-dev/cortex/internal/scheduler"
	"github.com/cobaltcore-dev/cortex/internal/scheduler/nova/api"
	"github.com/cobaltcore-dev/cortex/internal/sync/openstack/nova"
	"github.com/cobaltcore-dev/cortex/internal/sync/openstack/placement"
)

type FilterDisabledStep struct {
	scheduler.BaseStep[api.ExternalSchedulerRequest, scheduler.EmptyStepOpts]
}

// Get the name of this step, used for identification in config, logs, metrics, etc.
func (s *FilterDisabledStep) GetName() string { return "filter_disabled" }

// Only get hosts that are not disabled.
func (s *FilterDisabledStep) Run(traceLog *slog.Logger, request api.ExternalSchedulerRequest) (*scheduler.StepResult, error) {
	result := s.PrepareResult(request)
	// Don't run this filter for non-simulated requests yet.
	if !request.IsSimulated() {
		traceLog.Debug("skipping filter for non-simulated request")
		return result, nil
	}
	var computeHostsDisabled []string
	if _, err := s.DB.SelectTimed("scheduler-nova", &computeHostsDisabled, `
	    SELECT h.service_host
		FROM `+placement.Trait{}.TableName()+` rpt
		JOIN `+nova.Hypervisor{}.TableName()+` h
		ON h.id = rpt.resource_provider_uuid
		WHERE name = 'COMPUTE_STATUS_DISABLED'`,
		map[string]any{"az": request.Spec.Data.AvailabilityZone},
	); err != nil {
		return nil, err
	}
	lookupStr := strings.Join(computeHostsDisabled, ",")
	for host := range result.Activations {
		if !strings.Contains(lookupStr, host) {
			continue
		}
		delete(result.Activations, host)
		traceLog.Debug("filtering host due to disabled status", "host", host)
	}
	return result, nil
}
