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

type FilterHasAcceleratorsStep struct {
	lib.BaseStep[api.ExternalSchedulerRequest, lib.EmptyStepOpts]
}

// Get the name of this step, used for identification in config, logs, metrics, etc.
func (s *FilterHasAcceleratorsStep) GetName() string { return "filter_has_accelerators" }

// If requested, only get hosts with accelerators.
func (s *FilterHasAcceleratorsStep) Run(traceLog *slog.Logger, request api.ExternalSchedulerRequest) (*lib.StepResult, error) {
	result := s.PrepareResult(request)
	extraSpecs := request.Spec.Data.Flavor.Data.ExtraSpecs
	if _, ok := extraSpecs["accel:device_profile"]; !ok {
		traceLog.Debug("no accelerators requested")
		return result, nil
	}
	var computeHostsWithAccelerators []string
	if _, err := s.DB.SelectTimed("scheduler-nova", &computeHostsWithAccelerators, `
	    SELECT h.service_host
		FROM `+placement.Trait{}.TableName()+` rpt
		JOIN `+nova.Hypervisor{}.TableName()+` h
		ON h.id = rpt.resource_provider_uuid
		WHERE name = 'COMPUTE_ACCELERATORS'`,
		map[string]any{"az": request.Spec.Data.AvailabilityZone},
	); err != nil {
		return nil, err
	}
	lookupStr := strings.Join(computeHostsWithAccelerators, ",")
	for host := range result.Activations {
		if strings.Contains(lookupStr, host) {
			continue
		}
		delete(result.Activations, host)
		traceLog.Debug("filtering host which has no accelerators", "host", host)
	}
	return result, nil
}
