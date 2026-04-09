// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package filters

import (
	"context"
	"errors"
	"log/slog"
	"slices"

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

	// IgnoredReservationTypes is a list of reservation types to ignore when calculating capacity.
	// Valid values: "CommittedResourceReservation", "FailoverReservation"
	// When a reservation type is in this list, its capacity is not blocked.
	// Default: empty (all reservation types are considered)
	IgnoredReservationTypes []v1alpha1.ReservationType `json:"ignoredReservationTypes,omitempty"`

	// IgnoreAllocations skips subtracting current VM allocations from host capacity.
	// When true, only raw hardware capacity is considered (empty datacenter scenario).
	IgnoreAllocations bool `json:"ignoreAllocations,omitempty"`
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
	freeResourcesByHost := make(map[string]map[hv1.ResourceName]resource.Quantity)

	// The hypervisor resource auto-discovers its current utilization.
	// We can use the hypervisor status to calculate the total capacity
	// and then subtract the actual resource allocation from virtual machines.
	hvs := &hv1.HypervisorList{}
	if err := s.Client.List(context.Background(), hvs); err != nil {
		traceLog.Error("failed to list hypervisors", "error", err)
		return nil, err
	}
	for _, hv := range hvs.Items {
		if hv.Status.EffectiveCapacity == nil {
			traceLog.Warn("hypervisor with nil effective capacity, use capacity instead (overprovisioning not considered)", "host", hv.Name)
			freeResourcesByHost[hv.Name] = hv.Status.Capacity
		} else {
			// Start with the total effective capacity which is capacity * overcommit ratio.
			freeResourcesByHost[hv.Name] = hv.Status.EffectiveCapacity
		}

		// Subtract allocated resources (skip when ignoring allocations for empty-datacenter capacity queries).
		if !s.Options.IgnoreAllocations {
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

		// Check if this reservation type should be ignored
		if slices.Contains(s.Options.IgnoredReservationTypes, reservation.Spec.Type) {
			traceLog.Debug("ignoring reservation type", "type", reservation.Spec.Type, "reservation", reservation.Name)
			continue
		}

		// Handle reservation based on its type.
		switch reservation.Spec.Type {
		case v1alpha1.ReservationTypeCommittedResource, "": // Empty string for backward compatibility
			// Skip if no CommittedResourceReservation spec or no resource group set.
			if reservation.Spec.CommittedResourceReservation == nil || reservation.Spec.CommittedResourceReservation.ResourceGroup == "" {
				continue // Not handled by us (no resource group set).
			}

			// Check if this is a CR reservation scheduling request.
			// If so, we should NOT unlock any CR reservations to prevent overbooking.
			// CR capacity should only be unlocked for actual VM scheduling.
			intent, err := request.GetIntent()
			switch {
			case err == nil && intent == api.ReserveForCommittedResourceIntent:
				traceLog.Debug("keeping CR reservation locked for CR reservation scheduling",
					"reservation", reservation.Name,
					"intent", intent)
				// Don't continue - fall through to block the resources
			case !s.Options.LockReserved &&
				// For committed resource reservations: unlock resources only if:
				// 1. Project ID matches
				// 2. ResourceGroup matches the flavor's hw_version
				reservation.Spec.CommittedResourceReservation.ProjectID == request.Spec.Data.ProjectID &&
				reservation.Spec.CommittedResourceReservation.ResourceGroup == request.Spec.Data.Flavor.Data.ExtraSpecs["hw_version"]:
				traceLog.Info("unlocking resources reserved by matching committed resource reservation with allocation",
					"reservation", reservation.Name,
					"instanceUUID", request.Spec.Data.InstanceUUID,
					"projectID", request.Spec.Data.ProjectID,
					"resourceGroup", reservation.Spec.CommittedResourceReservation.ResourceGroup)
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

		// Block resources on BOTH Spec.TargetHost (desired) AND Status.Host (actual).
		// This ensures capacity is blocked during the transition period when a reservation
		// is being placed (TargetHost set) and after it's placed (Host set).
		// If both are the same, we only subtract once.
		hostsToBlock := make(map[string]struct{})
		if reservation.Spec.TargetHost != "" {
			hostsToBlock[reservation.Spec.TargetHost] = struct{}{}
		}
		if reservation.Status.Host != "" {
			hostsToBlock[reservation.Status.Host] = struct{}{}
		}
		if len(hostsToBlock) == 0 {
			traceLog.Debug("skipping reservation with no host", "reservation", reservation.Name)
			continue
		}

		// For CR reservations with allocations, calculate remaining (unallocated) resources to block.
		// This prevents double-blocking of resources already consumed by running instances.
		var resourcesToBlock map[hv1.ResourceName]resource.Quantity
		if reservation.Spec.Type == v1alpha1.ReservationTypeCommittedResource &&
			// if the reservation is not being migrated, block only unused resources
			reservation.Spec.TargetHost == reservation.Status.Host &&
			reservation.Spec.CommittedResourceReservation != nil &&
			reservation.Status.CommittedResourceReservation != nil &&
			len(reservation.Spec.CommittedResourceReservation.Allocations) > 0 &&
			len(reservation.Status.CommittedResourceReservation.Allocations) > 0 {
			// Start with full reservation resources
			resourcesToBlock = make(map[hv1.ResourceName]resource.Quantity)
			for k, v := range reservation.Spec.Resources {
				resourcesToBlock[k] = v.DeepCopy()
			}

			// Subtract already-allocated resources because those consume already resources on the host
			for instanceUUID, allocation := range reservation.Spec.CommittedResourceReservation.Allocations {
				// Only subtract if allocation is already present in status (VM is actually running)
				if _, isRunning := reservation.Status.CommittedResourceReservation.Allocations[instanceUUID]; !isRunning {
					continue
				}

				for resourceName, quantity := range allocation.Resources {
					if current, ok := resourcesToBlock[resourceName]; ok {
						current.Sub(quantity)
						resourcesToBlock[resourceName] = current
						traceLog.Debug("subtracting allocated resources from reservation",
							"reservation", reservation.Name,
							"instanceUUID", instanceUUID,
							"resource", resourceName,
							"quantity", quantity.String())
					}
				}
			}
		} else {
			// For other reservation types or CR without allocations, block full resources
			resourcesToBlock = reservation.Spec.Resources
		}

		// Block the calculated resources on each host
		for host := range hostsToBlock {
			// Skip hosts that don't have a corresponding Hypervisor resource.
			if _, hostExists := freeResourcesByHost[host]; !hostExists {
				traceLog.Debug("skipping reservation for unknown host",
					"reservation", reservation.Name,
					"host", host)
				continue
			}
			if cpu, ok := resourcesToBlock["cpu"]; ok {
				if freeCPU, exists := freeResourcesByHost[host]["cpu"]; exists {
					freeCPU.Sub(cpu)
					if freeCPU.Value() < 0 {
						traceLog.Warn("negative free CPU after blocking reservation",
							"host", host,
							"reservation", reservation.Name,
							"reservationType", reservation.Spec.Type,
							"freeCPU", freeCPU.String(),
							"blocked", cpu.String())
						freeCPU = resource.MustParse("0")
					}
					freeResourcesByHost[host]["cpu"] = freeCPU
				}
			}
			if memory, ok := resourcesToBlock["memory"]; ok {
				if freeMemory, exists := freeResourcesByHost[host]["memory"]; exists {
					freeMemory.Sub(memory)
					if freeMemory.Value() < 0 {
						traceLog.Warn("negative free memory after blocking reservation",
							"host", host,
							"reservation", reservation.Name,
							"reservationType", reservation.Spec.Type,
							"freeMemory", freeMemory.String(),
							"blocked", memory.String())
						freeMemory = resource.MustParse("0")
					}
					freeResourcesByHost[host]["memory"] = freeMemory
				}
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
