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

type FilterHasEnoughCapacityOpts struct {
	// If reserved space should be locked even for matching requests.
	LockReserved bool `json:"lockReserved"`
}

func (FilterHasEnoughCapacityOpts) Validate() error { return nil }

type FilterHasEnoughCapacity struct {
	scheduler.BaseStep[api.ExternalSchedulerRequest, FilterHasEnoughCapacityOpts]

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
	for _, reservation := range reservations.Items {
		if reservation.Status.Phase != v1alpha1.ComputeReservationStatusPhaseActive {
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
