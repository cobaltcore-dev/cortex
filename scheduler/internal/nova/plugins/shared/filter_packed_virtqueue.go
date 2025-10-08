// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package shared

import (
	"log/slog"
	"strings"

	"github.com/cobaltcore-dev/cortex/internal/sync/openstack/nova"
	"github.com/cobaltcore-dev/cortex/internal/sync/openstack/placement"
	"github.com/cobaltcore-dev/cortex/scheduler/internal/lib"
	"github.com/cobaltcore-dev/cortex/scheduler/internal/nova/api"
)

type FilterPackedVirtqueueStep struct {
	lib.BaseStep[api.PipelineRequest, lib.EmptyStepOpts]
}

// Get the name of this step, used for identification in config, logs, metrics, etc.
func (s *FilterPackedVirtqueueStep) GetName() string { return "filter_packed_virtqueue" }

// If requested, only get hosts with packed virtqueues.
func (s *FilterPackedVirtqueueStep) Run(traceLog *slog.Logger, request api.PipelineRequest) (*lib.StepResult, error) {
	result := s.PrepareResult(request)
	// We don't care about the value.
	_, reqInSpecs := request.Spec.Data.Flavor.Data.ExtraSpecs["hw:virtio_packed_ring"]
	_, reqInProps := request.Spec.Data.Image.Data.Properties.Data["hw_virtio_packed_ring"]
	if !reqInSpecs && !reqInProps {
		return result, nil // No packed virtqueue requested, nothing to filter.
	}
	var computeHostsWithPackedVirtqueues []string
	if _, err := s.DB.SelectTimed("scheduler-nova", &computeHostsWithPackedVirtqueues, `
	    SELECT h.service_host
		FROM `+placement.Trait{}.TableName()+` rpt
		JOIN `+nova.Hypervisor{}.TableName()+` h
		ON h.id = rpt.resource_provider_uuid
		WHERE name = 'COMPUTE_NET_VIRTIO_PACKED'`,
		map[string]any{"az": request.Spec.Data.AvailabilityZone},
	); err != nil {
		return nil, err
	}
	lookupStr := strings.Join(computeHostsWithPackedVirtqueues, ",")
	for host := range result.Activations {
		if strings.Contains(lookupStr, host) {
			continue
		}
		delete(result.Activations, host)
		traceLog.Debug("filtering host which has no packed virtqueues", "host", host)
	}
	return result, nil
}
