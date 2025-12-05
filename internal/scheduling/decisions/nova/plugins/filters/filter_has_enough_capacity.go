// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package filters

import (
	"context"
	"errors"
	"log/slog"

	api "github.com/cobaltcore-dev/cortex/api/delegation/nova"
	"github.com/cobaltcore-dev/cortex/api/v1alpha1"
	"github.com/cobaltcore-dev/cortex/internal/knowledge/extractor/plugins/shared"
	"github.com/cobaltcore-dev/cortex/internal/scheduling/lib"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

type FilterHasEnoughCapacityOpts struct {
	// If reserved space should be locked even for matching requests.
	LockReserved bool `json:"lockReserved"`
}

func (FilterHasEnoughCapacityOpts) Validate() error { return nil }

type FilterHasEnoughCapacity struct {
	lib.BaseStep[api.ExternalSchedulerRequest, FilterHasEnoughCapacityOpts]
}

// Filter hosts that don't have enough capacity to run the requested flavor.
//
// This filter takes the capacity of the hosts and subtracts from it:
//   - The resources currently used by VMs.
//   - The resources reserved by active Reservations.
//
// In case the project and flavor match, space reserved is unlocked (slotting).
//
// Please note that, if num_instances is larger than 1, there needs to be enough
// capacity to place all instances on the same host. This limitation is necessary
// because we can't spread out instances, as the final set of valid hosts is not
// known at this point.
//
// Please also note that disk space is currently not considered by this filter.
func (s *FilterHasEnoughCapacity) Run(traceLog *slog.Logger, request api.ExternalSchedulerRequest) (*lib.StepResult, error) {
	result := s.PrepareResult(request)
	knowledge := &v1alpha1.Knowledge{}
	if err := s.Client.Get(
		context.Background(),
		client.ObjectKey{Name: "host-utilization"},
		knowledge,
	); err != nil {
		return nil, err
	}
	hostUtilizations, err := v1alpha1.
		UnboxFeatureList[shared.HostUtilization](knowledge.Status.Raw)
	if err != nil {
		return nil, err
	}
	var reservations v1alpha1.ReservationList
	ctx := context.Background()
	if err := s.Client.List(ctx, &reservations); err != nil {
		return nil, err
	}
	// Resources reserved by hosts.
	vcpusReserved := make(map[string]uint64)  // in vCPUs
	memoryReserved := make(map[string]uint64) // in MB
	for _, reservation := range reservations.Items {
		if reservation.Status.Phase != v1alpha1.ReservationStatusPhaseActive {
			continue // Only consider active reservations.
		}
		if reservation.Spec.Scheduler.CortexNova == nil {
			continue // Not handled by us.
		}
		// If the requested vm matches this reservation, free the resources.
		if !s.Options.LockReserved &&
			reservation.Spec.Scheduler.CortexNova.ProjectID == request.Spec.Data.ProjectID &&
			reservation.Spec.Scheduler.CortexNova.FlavorName == request.Spec.Data.Flavor.Data.Name {
			traceLog.Info("unlocking resources reserved by matching reservation", "reservation", reservation.Name)
			continue
		}
		host := reservation.Status.Host
		if cpu, ok := reservation.Spec.Requests["cpu"]; ok {
			vcpusReserved[host] += cpu.AsDec().UnscaledBig().Uint64()
		}
		if memory, ok := reservation.Spec.Requests["memory"]; ok {
			memoryReserved[host] += memory.AsDec().UnscaledBig().Uint64() / 1000000 // MB
		}
		// Disk is currently not considered.
	}
	traceLog.Debug(
		"reserved resources",
		"vcpus", vcpusReserved,
		"memory", memoryReserved,
	)
	hostsEncountered := map[string]struct{}{}
	for _, utilization := range hostUtilizations {
		hostsEncountered[utilization.ComputeHost] = struct{}{}
		vCPUsAllocatable := uint64(utilization.TotalVCPUsAllocatable)
		if reserved, ok := vcpusReserved[utilization.ComputeHost]; ok {
			vCPUsAllocatable -= reserved
		}
		if request.Spec.Data.Flavor.Data.VCPUs == 0 {
			return nil, errors.New("flavor has 0 vcpus")
		}
		vcpuSlots := vCPUsAllocatable / request.Spec.Data.Flavor.Data.VCPUs // floored.
		if vcpuSlots < request.Spec.Data.NumInstances {
			traceLog.Debug(
				"Filtering host due to insufficient VCPU capacity",
				slog.String("host", utilization.ComputeHost),
				slog.Uint64("requested_vcpus", request.Spec.Data.Flavor.Data.VCPUs),
				slog.Uint64("requested_instances", request.Spec.Data.NumInstances),
				slog.Float64("available_vcpus", utilization.TotalVCPUsAllocatable),
			)
			delete(result.Activations, utilization.ComputeHost)
			continue
		}
		memoryAllocatableMB := uint64(utilization.TotalRAMAllocatableMB)
		if reserved, ok := memoryReserved[utilization.ComputeHost]; ok {
			memoryAllocatableMB -= reserved
		}
		if request.Spec.Data.Flavor.Data.MemoryMB == 0 {
			return nil, errors.New("flavor has 0 memory")
		}
		memorySlots := memoryAllocatableMB / request.Spec.Data.Flavor.Data.MemoryMB // floored.
		if memorySlots < request.Spec.Data.NumInstances {
			traceLog.Debug(
				"Filtering host due to insufficient RAM capacity",
				slog.String("host", utilization.ComputeHost),
				slog.Uint64("requested_mb", request.Spec.Data.Flavor.Data.MemoryMB),
				slog.Uint64("requested_instances", request.Spec.Data.NumInstances),
				slog.Float64("available_mb", utilization.TotalRAMAllocatableMB),
			)
			delete(result.Activations, utilization.ComputeHost)
			continue
		}
		// Disk is currently not considered.
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
