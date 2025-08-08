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

type FilterPackedVirtqueueStep struct {
	scheduler.BaseStep[api.ExternalSchedulerRequest, scheduler.EmptyStepOpts]
}

// Get the name of this step, used for identification in config, logs, metrics, etc.
func (s *FilterPackedVirtqueueStep) GetName() string { return "filter_packed_virtqueue" }

// If requested, only get hosts with packed virtqueues.
func (s *FilterPackedVirtqueueStep) Run(traceLog *slog.Logger, request api.ExternalSchedulerRequest) (*scheduler.StepResult, error) {
	result := s.PrepareResult(request)
	extraSpecs := request.Spec.Data.Flavor.Data.ExtraSpecs
	if _, ok := extraSpecs["hw:virtio_packed_ring"]; !ok {
		traceLog.Debug("no packed virtqueues requested")
		return result, nil
	}
	properties := request.Spec.Data.Image.Properties
	if _, ok := properties["hw_virtio_packed_ring"]; !ok {
		traceLog.Debug("no packed virtqueues requested in image properties")
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
		if !strings.Contains(lookupStr, host) {
			continue
		}
		delete(result.Activations, host)
		traceLog.Debug("filtering host which has no accelerators", "host", host)
	}
	return result, nil
}
