// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package shared

import (
	"context"
	"log/slog"

	"github.com/cobaltcore-dev/cortex/internal/conf"
	"github.com/cobaltcore-dev/cortex/internal/db"
	"github.com/cobaltcore-dev/cortex/internal/extractor/plugins/shared"
	"github.com/cobaltcore-dev/cortex/internal/scheduler"
	"github.com/cobaltcore-dev/cortex/internal/scheduler/nova/api"
	"github.com/cobaltcore-dev/cortex/reservations/api/v1alpha1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

type FilterHasEnoughCapacity struct {
	scheduler.BaseStep[api.ExternalSchedulerRequest, scheduler.EmptyStepOpts]

	// Kubernetes client.
	Client client.Client
}

func (s *FilterHasEnoughCapacity) Init(alias string, db db.DB, opts conf.RawOpts) error {
	if err := s.BaseStep.Init(alias, db, opts); err != nil {
		return err
	}
	if s.Client != nil {
		return nil // Already initialized.
	}
	scheme, err := v1alpha1.SchemeBuilder.Build()
	if err != nil {
		return err
	}
	clientConfig, err := ctrl.GetConfig()
	if err != nil {
		return err
	}
	cl, err := client.New(clientConfig, client.Options{Scheme: scheme})
	if err != nil {
		return err
	}
	s.Client = cl
	return nil
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
	var reservations v1alpha1.ComputeReservationList
	ctx := context.Background()
	if err := s.Client.List(ctx, &reservations); err != nil {
		return nil, err
	}
	// Resources reserved by hosts.
	vcpusReserved := make(map[string]uint64)  // in vCPUs
	memoryReserved := make(map[string]uint64) // in MB
	diskReserved := make(map[string]uint64)   // in GB
	for _, reservation := range reservations.Items {
		if reservation.Status.Phase != v1alpha1.ComputeReservationStatusPhaseActive {
			continue // Only consider active reservations.
		}
		if reservation.Spec.Kind != v1alpha1.ComputeReservationSpecKindInstance {
			continue // Not an instance reservation, skip it.
		}
		host := reservation.Status.Host
		instance := reservation.Spec.Instance
		vcpusReserved[host] += instance.VCPUs.AsDec().UnscaledBig().Uint64()
		memoryReserved[host] += instance.Memory.AsDec().UnscaledBig().Uint64() / 1000000
		diskReserved[host] += instance.Disk.AsDec().UnscaledBig().Uint64() / 1000000000
	}
	traceLog.Debug(
		"reserved resources",
		"vcpus", vcpusReserved,
		"memory", memoryReserved,
		"disk", diskReserved,
	)
	hostsEncountered := map[string]struct{}{}
	for _, utilization := range hostUtilizations {
		hostsEncountered[utilization.ComputeHost] = struct{}{}
		vCPUsAllocatable := uint64(utilization.TotalVCPUsAllocatable)
		if reserved, ok := vcpusReserved[utilization.ComputeHost]; ok {
			vCPUsAllocatable -= reserved
		}
		if vCPUsAllocatable < request.Spec.Data.Flavor.Data.VCPUs {
			slog.Debug(
				"Filtering host due to insufficient VCPU capacity",
				slog.String("host", utilization.ComputeHost),
				slog.Uint64("requested_vcpus", request.Spec.Data.Flavor.Data.VCPUs),
				slog.Float64("available_vcpus", utilization.TotalVCPUsAllocatable),
			)
			delete(result.Activations, utilization.ComputeHost)
			continue
		}
		memoryAllocatableMB := uint64(utilization.TotalRAMAllocatableMB)
		if reserved, ok := memoryReserved[utilization.ComputeHost]; ok {
			memoryAllocatableMB -= reserved
		}
		if memoryAllocatableMB < request.Spec.Data.Flavor.Data.MemoryMB {
			slog.Debug(
				"Filtering host due to insufficient RAM capacity",
				slog.String("host", utilization.ComputeHost),
				slog.Uint64("requested_mb", request.Spec.Data.Flavor.Data.MemoryMB),
				slog.Float64("available_mb", utilization.TotalRAMAllocatableMB),
			)
			delete(result.Activations, utilization.ComputeHost)
			continue
		}
		diskAllocatableGB := uint64(utilization.TotalDiskAllocatableGB)
		if reserved, ok := diskReserved[utilization.ComputeHost]; ok {
			diskAllocatableGB -= reserved
		}
		if diskAllocatableGB < request.Spec.Data.Flavor.Data.RootGB {
			slog.Debug(
				"Filtering host due to insufficient Disk capacity",
				slog.String("host", utilization.ComputeHost),
				slog.Uint64("requested_gb", request.Spec.Data.Flavor.Data.RootGB),
				slog.Float64("available_gb", utilization.TotalDiskAllocatableGB),
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
