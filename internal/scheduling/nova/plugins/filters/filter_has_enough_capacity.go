// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package filters

import (
	"context"
	"errors"
	"log/slog"

	api "github.com/cobaltcore-dev/cortex/api/external/nova"
	"github.com/cobaltcore-dev/cortex/api/v1alpha1"
	"github.com/cobaltcore-dev/cortex/internal/scheduling/lib"
	hv1 "github.com/cobaltcore-dev/openstack-hypervisor-operator/api/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/api/resource"
)

type FilterHasEnoughCapacityOpts struct {
	// If reserved space should be locked even for matching requests.
	LockReserved bool `json:"lockReserved"`
}

func (FilterHasEnoughCapacityOpts) Validate() error { return nil }

type FilterHasEnoughCapacity struct {
	lib.BaseFilter[api.ExternalSchedulerRequest, FilterHasEnoughCapacityOpts]
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
func (s *FilterHasEnoughCapacity) Run(traceLog *slog.Logger, request api.ExternalSchedulerRequest) (*lib.FilterWeigherPipelineStepResult, error) {
	result := s.IncludeAllHostsFromRequest(request)

	// This map holds the free resources per host.
	freeResourcesByHost := make(map[string]map[string]resource.Quantity)

	// The hypervisor resource auto-discovers its current utilization.
	// We can use the hypervisor status to calculate the total capacity
	// and then subtract the actual resource allocation from virtual machines.
	hvs := &hv1.HypervisorList{}
	if err := s.Client.List(context.Background(), hvs); err != nil {
		traceLog.Error("failed to list hypervisors", "error", err)
		return nil, err
	}
	for _, hv := range hvs.Items {
		// Start with the total capacity.
		freeResourcesByHost[hv.Name] = hv.Status.Capacity

		// Subtract allocated resources.
		for resourceName, allocated := range hv.Status.Allocation {
			free, ok := freeResourcesByHost[hv.Name][resourceName]
			if !ok {
				traceLog.Error(
					"hypervisor with allocation for unknown resource",
					"host", hv.Name, "resource", resourceName,
				)
				continue
			}
			free.Sub(allocated)
			freeResourcesByHost[hv.Name][resourceName] = free
		}
	}

	// Subtract reserved resources by Reservations.
	var reservations v1alpha1.ReservationList
	if err := s.Client.List(context.Background(), &reservations); err != nil {
		return nil, err
	}
	for _, reservation := range reservations.Items {
		if !meta.IsStatusConditionTrue(reservation.Status.Conditions, v1alpha1.ReservationConditionReady) {
			continue // Only consider active reservations (Ready=True).
		}

		// Handle reservation based on its type.
		switch reservation.Spec.Type {
		case v1alpha1.ReservationTypeCommittedResource, "": // Empty string for backward compatibility
			// Skip if no CommittedResourceReservation spec or no resource name set.
			if reservation.Spec.CommittedResourceReservation == nil || reservation.Spec.CommittedResourceReservation.ResourceName == "" {
				continue // Not handled by us (no resource name set).
			}
			// For committed resource reservations: if the requested VM matches this reservation, free the resources (slotting).
			if !s.Options.LockReserved &&
				reservation.Spec.CommittedResourceReservation.ProjectID == request.Spec.Data.ProjectID &&
				reservation.Spec.CommittedResourceReservation.ResourceName == request.Spec.Data.Flavor.Data.Name {
				traceLog.Info("unlocking resources reserved by matching committed resource reservation", "reservation", reservation.Name)
				continue
			}
		case v1alpha1.ReservationTypeFailover:
			// For failover reservations: if the requested VM is contained in the allocations map
			// AND this is an evacuation request, unlock the resources.
			// We only unlock during evacuations because:
			// 1. Failover reservations are specifically for HA/evacuation scenarios.
			// 2. During live migrations or other operations, we don't want to use failover capacity.
			// Note: we cannot use failover reservations from other VMs, as that can invalidate our HA guarantees.
			intent, err := request.GetIntent()
			if err == nil && intent == api.EvacuateIntent {
				if reservation.Status.FailoverReservation != nil {
					if _, contained := reservation.Status.FailoverReservation.Allocations[request.Spec.Data.InstanceUUID]; contained {
						traceLog.Info("unlocking resources reserved by failover reservation for VM in allocations (evacuation)",
							"reservation", reservation.Name,
							"instanceUUID", request.Spec.Data.InstanceUUID)
						continue
					}
				}
			}
			traceLog.Debug("processing failover reservation", "reservation", reservation.Name)
		}

		// Block resources on BOTH Spec.TargetHost (desired) AND Status.ObservedHost (actual).
		// This ensures capacity is blocked during the transition period when a reservation
		// is being placed (TargetHost set) and after it's placed (ObservedHost set).
		// If both are the same, we only subtract once.
		hostsToBlock := make(map[string]struct{})
		if reservation.Spec.TargetHost != "" {
			hostsToBlock[reservation.Spec.TargetHost] = struct{}{}
		}
		if reservation.Status.ObservedHost != "" {
			hostsToBlock[reservation.Status.ObservedHost] = struct{}{}
		}
		if len(hostsToBlock) == 0 {
			traceLog.Debug("skipping reservation with no host", "reservation", reservation.Name)
			continue
		}

		for host := range hostsToBlock {
			if cpu, ok := reservation.Spec.Resources["cpu"]; ok {
				freeCPU := freeResourcesByHost[host]["cpu"]
				freeCPU.Sub(cpu)
				freeResourcesByHost[host]["cpu"] = freeCPU
			}
			if memory, ok := reservation.Spec.Resources["memory"]; ok {
				freeMemory := freeResourcesByHost[host]["memory"]
				freeMemory.Sub(memory)
				freeResourcesByHost[host]["memory"] = freeMemory
			}
		}
	}

	hostsEncountered := make(map[string]struct{})
	for host, free := range freeResourcesByHost {
		hostsEncountered[host] = struct{}{}

		// Check cpu capacity.
		if request.Spec.Data.Flavor.Data.VCPUs == 0 {
			return nil, errors.New("flavor has 0 vcpus")
		}
		freeCPU, ok := free["cpu"]
		if !ok || freeCPU.Value() < 0 {
			traceLog.Error(
				"host with invalid CPU capacity",
				"host", host, "freeCPU", freeCPU.String(),
			)
			continue
		}
		// Calculate how many instances can fit on this host, based on cpu.
		//nolint:gosec // We're checking for underflows above (< 0).
		vcpuSlots := uint64(freeCPU.Value()) /
			request.Spec.Data.Flavor.Data.VCPUs
		if vcpuSlots < request.Spec.Data.NumInstances {
			traceLog.Info(
				"filtering host due to insufficient CPU capacity",
				"host", host, "requested", request.Spec.Data.Flavor.Data.VCPUs,
				"available", freeCPU.String(),
			)
			delete(result.Activations, host)
			continue
		}

		// Check memory capacity.
		if request.Spec.Data.Flavor.Data.MemoryMB == 0 {
			return nil, errors.New("flavor has 0 memory")
		}
		freeMemory, ok := free["memory"]
		if !ok || freeMemory.Value() < 0 {
			traceLog.Error(
				"host with invalid memory capacity",
				"host", host, "freeMemory", freeMemory.String(),
			)
			continue
		}
		// Calculate how many instances can fit on this host, based on memory.
		// Note: according to the OpenStack docs, the memory is in MB, not MiB.
		// See: https://docs.openstack.org/nova/latest/user/flavors.html
		//nolint:gosec // We're checking for underflows above (< 0).
		memorySlots := uint64(freeMemory.Value()/1_000_000 /* MB */) /
			request.Spec.Data.Flavor.Data.MemoryMB
		if memorySlots < request.Spec.Data.NumInstances {
			traceLog.Info(
				"filtering host due to insufficient RAM capacity",
				"host", host, "requested_mb", request.Spec.Data.Flavor.Data.MemoryMB,
				"available_mb", freeMemory.String(),
			)
			delete(result.Activations, host)
			continue
		}
		traceLog.Info(
			"host has enough capacity", "host", host,
			"requested_cpus", request.Spec.Data.Flavor.Data.VCPUs,
			"available_cpus", freeCPU.String(),
			"requested_memory_mb", request.Spec.Data.Flavor.Data.MemoryMB,
			"available_memory", freeMemory.String(),
		)
	}

	// Remove all hosts that weren't encountered.
	for host := range result.Activations {
		if _, ok := hostsEncountered[host]; !ok {
			delete(result.Activations, host)
			traceLog.Info(
				"removing host with unknown capacity",
				"host", host,
			)
		}
	}
	return result, nil
}

func init() {
	Index["filter_has_enough_capacity"] = func() NovaFilter { return &FilterHasEnoughCapacity{} }
}
