// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package shared

import (
	"log/slog"

	"github.com/cobaltcore-dev/cortex/internal/extractor/plugins/shared"
	"github.com/cobaltcore-dev/cortex/internal/scheduler"
	"github.com/cobaltcore-dev/cortex/internal/scheduler/nova/api"
)

type FilterHasEnoughCapacity struct {
	scheduler.BaseStep[api.ExternalSchedulerRequest, scheduler.EmptyStepOpts]
}

func (s *FilterHasEnoughCapacity) GetName() string { return "filter_has_enough_capacity" }

// Filter hosts that don't have enough capacity to run the requested flavor.
func (s *FilterHasEnoughCapacity) Run(traceLog *slog.Logger, request api.ExternalSchedulerRequest) (*scheduler.StepResult, error) {
	result := s.PrepareResult(request)
	var hostUtilizations []shared.HostUtilization
	group := "scheduler-nova"
	if _, err := s.DB.SelectTimed(
		group, &hostUtilizations, "SELECT * FROM "+shared.HostUtilization{}.TableName(),
	); err != nil {
		return nil, err
	}
	hostsEncountered := map[string]struct{}{}
	for _, utilization := range hostUtilizations {
		hostsEncountered[utilization.ComputeHost] = struct{}{}
		if int(utilization.TotalVCPUsAllocatable) < request.Spec.Data.Flavor.Data.VCPUs {
			slog.Debug(
				"Filtering host due to insufficient VCPU capacity",
				slog.String("host", utilization.ComputeHost),
				slog.Int("requested_vcpus", request.Spec.Data.Flavor.Data.VCPUs),
				slog.Int("available_vcpus", int(utilization.TotalVCPUsAllocatable)),
			)
			delete(result.Activations, utilization.ComputeHost)
			continue
		}
		if int(utilization.TotalMemoryAllocatableMB) < request.Spec.Data.Flavor.Data.MemoryMB {
			slog.Debug(
				"Filtering host due to insufficient RAM capacity",
				slog.String("host", utilization.ComputeHost),
				slog.Int("requested_mb", request.Spec.Data.Flavor.Data.MemoryMB),
				slog.Int("available_mb", int(utilization.TotalMemoryAllocatableMB)),
			)
			delete(result.Activations, utilization.ComputeHost)
			continue
		}
		if int(utilization.TotalDiskAllocatableGB) < request.Spec.Data.Flavor.Data.RootGB {
			slog.Debug(
				"Filtering host due to insufficient Disk capacity",
				slog.String("host", utilization.ComputeHost),
				slog.Int("requested_gb", request.Spec.Data.Flavor.Data.RootGB),
				slog.Int("available_gb", int(utilization.TotalDiskAllocatableGB)),
			)
			delete(result.Activations, utilization.ComputeHost)
			continue
		}
	}
	// Remove all hosts that weren't encountered.
	for host := range result.Activations {
		if _, ok := hostsEncountered[host]; !ok {
			delete(result.Activations, host)
			traceLog.Debug(
				"removing host with unknown capacity",
				"host", host,
			)
		}
	}
	return result, nil
}
