// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package filters

import (
	"context"
	"errors"
	"slices"

	"github.com/go-logr/logr"

	api "github.com/cobaltcore-dev/cortex/api/external/nova"
	"github.com/cobaltcore-dev/cortex/api/v1alpha1"
	"github.com/cobaltcore-dev/cortex/internal/scheduling/compute"
	"github.com/cobaltcore-dev/cortex/internal/scheduling/lib"
	resv "github.com/cobaltcore-dev/cortex/internal/scheduling/reservations/accounting"
	hv1 "github.com/cobaltcore-dev/openstack-hypervisor-operator/api/v1"
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
	lib.BaseFilter[compute.PlacementRequest, FilterHasEnoughCapacityOpts]
}

// Filter hosts that don't have enough capacity to run the requested flavor.
//
// This filter takes the capacity of the hosts and subtracts from it:
//   - The resources currently used by VMs.
//   - The resources reserved by active Reservations.
//
// In case the project and flavor match, space reserved is unlocked (slotting).
//
// Capacity accounting uses two sources: hv.Status.Allocation (aggregate real-time usage of
// all running VMs) and Reservation.Status.Allocations (which VMs are confirmed on a slot,
// maintained by the reservation controller with a one-reconcile-cycle lag). During the window
// between a VM starting and the reservation controller reconciling, a VM appears in both
// sources — a conservative transient over-count that self-corrects on the next reconcile.
//
// During a CR reservation migration (TargetHost != Status.Host), both the source and target
// host are blocked with the full slot. The source block is intentionally conservative to
// preserve rollback capacity if the migration fails.
//
// Please note that, if num_instances is larger than 1, there needs to be enough
// capacity to place all instances on the same host. This limitation is necessary
// because we can't spread out instances, as the final set of valid hosts is not
// known at this point.
//
// Please also note that disk space is currently not considered by this filter.
func (s *FilterHasEnoughCapacity) Run(traceLog logr.Logger, request compute.PlacementRequest) (*lib.FilterWeigherPipelineStepResult, error) {
	opts := request.GetOptions()
	result := s.IncludeAllHostsFromRequest(request)

	// Merge call-time options with static step config.
	ignoreAllocations := s.Options.IgnoreAllocations || opts.AssumeEmptyHosts
	ignoredReservationTypes := slices.Concat(s.Options.IgnoredReservationTypes, opts.IgnoredReservationTypes)

	// This map holds the free resources per host.
	freeResourcesByHost := make(map[string]map[hv1.ResourceName]resource.Quantity)

	// The hypervisor resource auto-discovers its current utilization.
	// We can use the hypervisor status to calculate the total capacity
	// and then subtract the actual resource allocation from virtual machines.
	hvs := &hv1.HypervisorList{}
	if err := s.Client.List(context.Background(), hvs); err != nil {
		traceLog.Error(err, "failed to list hypervisors")
		return nil, err
	}
	for _, hv := range hvs.Items {
		if _, ok := result.Activations[hv.Name]; !ok {
			continue
		}
		var sourceMap map[hv1.ResourceName]resource.Quantity
		if hv.Status.EffectiveCapacity == nil {
			traceLog.V(1).Info("hypervisor with nil effective capacity, use capacity instead (overprovisioning not considered)", "host", hv.Name)
			sourceMap = hv.Status.Capacity
		} else {
			// Start with the total effective capacity which is capacity * overcommit ratio.
			sourceMap = hv.Status.EffectiveCapacity
		}
		// Deep-copy the map to avoid mutating the informer cache.
		copied := make(map[hv1.ResourceName]resource.Quantity, len(sourceMap))
		for k, v := range sourceMap {
			copied[k] = v.DeepCopy()
		}
		freeResourcesByHost[hv.Name] = copied

		// Subtract allocated resources (skip when ignoring allocations for empty-datacenter capacity queries).
		if !ignoreAllocations {
			for resourceName, allocated := range hv.Status.Allocation {
				free, ok := freeResourcesByHost[hv.Name][resourceName]
				if !ok {
					traceLog.Error(
						nil,
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
	// Per-(VM, host) in-flight rule (ADR 8, UC2): once a VM lands on a host, its
	// smallest in-flight hold there is already counted in hv.Status.Allocation,
	// so skip it to avoid double-counting. Compute the skip set per host.
	skipInFlightByHost := make(map[string]map[string]struct{})
	for _, hv := range hvs.Items {
		if skip := inFlightHoldsToSkip(reservations.Items, hv); len(skip) > 0 {
			skipInFlightByHost[hv.Name] = skip
		}
	}
	for _, reservation := range reservations.Items {
		// Check if this reservation type should be ignored — applies regardless of ready state.
		if slices.Contains(ignoredReservationTypes, reservation.Spec.Type) {
			traceLog.V(1).Info("ignoring reservation type", "type", reservation.Spec.Type, "reservation", reservation.Name)
			continue
		}
		if ownsInFlightHold(request, reservation) {
			traceLog.V(1).Info("ignoring the request's own in-flight reservation", "reservation", reservation.Name)
			continue
		}

		if !reservation.IsReady() {
			if reservation.Spec.TargetHost == "" && reservation.Status.Host == "" {
				continue // not placed yet, nothing to block
			}
			// TargetHost is set but Ready has not been synced yet: the reservation controller
			// wrote the host in reconcile cycle 1 but hasn't completed cycle 2 (status sync).
			// Block the full slot now to prevent a concurrent scheduling call from picking the
			// same host. Unlock logic is intentionally skipped — an unconfirmed slot must not
			// be unlocked for any VM or project.
			traceLog.V(1).Info("reservation has target host set but is not yet ready — blocking host to prevent double-booking",
				"reservation", reservation.Name,
				"targetHost", reservation.Spec.TargetHost,
				"statusHost", reservation.Status.Host,
			)
		} else {
			// Ready reservation: apply type-specific unlock logic.
			switch reservation.Spec.Type {
			case v1alpha1.ReservationTypeCommittedResource, "": // Empty string for backward compatibility
				// Skip if no CommittedResourceReservation spec or no resource group set.
				if reservation.Spec.CommittedResourceReservation == nil || reservation.Spec.CommittedResourceReservation.ResourceGroup == "" {
					continue // Not handled by us (no resource group set).
				}

				// Check if this is a CR reservation scheduling request.
				// If so, we should NOT unlock any CR reservations to prevent overbooking.
				// CR capacity should only be unlocked for actual VM scheduling.
				switch {
				case request.Intent == api.ReserveForCommittedResourceIntent:
					traceLog.V(1).Info("keeping CR reservation locked for CR reservation scheduling",
						"reservation", reservation.Name,
						"intent", request.Intent)
					// Don't continue - fall through to block the resources
				case !s.Options.LockReserved && !opts.LockReservations &&
					// For committed resource reservations: unlock resources only if:
					// 1. Project ID matches
					// 2. ResourceGroup matches the flavor's hw_version
					reservation.Spec.CommittedResourceReservation.MatchesGroup(request.ProjectID, request.HardwareVersion):
					traceLog.V(1).Info("unlocking resources reserved by matching committed resource reservation with allocation",
						"reservation", reservation.Name,
						"instanceUUID", request.InstanceUUID,
						"projectID", request.ProjectID,
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
				switch request.Intent {
				case api.EvacuateIntent:
					if reservation.Status.FailoverReservation != nil {
						if _, contained := reservation.Status.FailoverReservation.Allocations[request.InstanceUUID]; contained {
							traceLog.V(1).Info("unlocking resources reserved by failover reservation for VM in allocations (evacuation)",
								"reservation", reservation.Name,
								"instanceUUID", request.InstanceUUID)
							continue
						}
					}
				case api.ReuseFailoverReservationIntent:
					// Reuse check: the reservation already pre-blocks the right capacity for this VM.
					// Don't subtract it from free capacity to avoid double-counting.
					traceLog.V(1).Info("skipping failover reservation block for reuse compatibility check",
						"reservation", reservation.Name)
					continue
				}
				traceLog.V(1).Info("processing failover reservation", "reservation", reservation.Name)
			}
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
			traceLog.V(1).Info("skipping reservation with no host", "reservation", reservation.Name)
			continue
		}

		// CommittedResourceReservations: block = max(slot − confirmed VM resources, 0).
		// Confirmed VMs appear in hv.Status.Allocation; this subtracts only the unconfirmed remainder
		// to avoid double-counting. FailoverReservations: block = Spec.Resources (always fully blocked).
		resourcesToBlock := resv.UnusedReservationCapacity(&reservation, ignoreAllocations)

		// Block the calculated resources on each host
		for host := range hostsToBlock {
			// Skip hosts that don't have a corresponding Hypervisor resource.
			if _, hostExists := freeResourcesByHost[host]; !hostExists {
				traceLog.V(1).Info("skipping reservation for unknown host",
					"reservation", reservation.Name,
					"host", host)
				continue
			}
			// Skip the smallest in-flight hold of a VM already landed on this
			// host; its resources are already in hv.Status.Allocation.
			if skip, ok := skipInFlightByHost[host]; ok {
				if _, drop := skip[reservationKey(&reservation)]; drop {
					traceLog.V(1).Info("skipping in-flight hold already counted in host allocation",
						"reservation", reservation.Name, "host", host)
					continue
				}
			}
			if cpu, ok := resourcesToBlock["cpu"]; ok {
				if freeCPU, exists := freeResourcesByHost[host]["cpu"]; exists {
					freeCPU.Sub(cpu)
					if freeCPU.Value() < 0 {
						traceLog.V(1).Info("negative free CPU after blocking reservation",
							"host", host,
							"reservation", reservation.Name,
							"reservationType", reservation.Spec.Type,
							"freeCPU", freeCPU.String(),
							"blocked", cpu.String())
						freeCPU = resource.Quantity{}
					}
					freeResourcesByHost[host]["cpu"] = freeCPU
				}
			}
			if memory, ok := resourcesToBlock["memory"]; ok {
				if freeMemory, exists := freeResourcesByHost[host]["memory"]; exists {
					freeMemory.Sub(memory)
					if freeMemory.Value() < 0 {
						traceLog.V(1).Info("negative free memory after blocking reservation",
							"host", host,
							"reservation", reservation.Name,
							"reservationType", reservation.Spec.Type,
							"freeMemory", freeMemory.String(),
							"blocked", memory.String())
						freeMemory = resource.Quantity{}
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
		if request.Flavor.VCPUs == 0 {
			return nil, errors.New("flavor has 0 vcpus")
		}
		freeCPU, ok := free["cpu"]
		if !ok || freeCPU.Value() < 0 {
			traceLog.V(1).Info(
				"host with invalid CPU capacity",
				"host", host, "freeCPU", freeCPU.String(),
			)
			delete(result.Activations, host) // host is already overloaded, remove from activations
			continue
		}
		// Calculate how many instances can fit on this host, based on cpu.
		//nolint:gosec // We're checking for underflows above (< 0).
		vcpuSlots := uint64(freeCPU.Value()) /
			request.Flavor.VCPUs
		if vcpuSlots < request.NumInstances {
			traceLog.V(1).Info(
				"filtering host due to insufficient CPU capacity",
				"host", host, "requested", request.Flavor.VCPUs,
				"available", freeCPU.String(),
			)
			delete(result.Activations, host)
			continue
		}

		// Check memory capacity.
		if request.Flavor.MemoryMB == 0 {
			return nil, errors.New("flavor has 0 memory")
		}
		freeMemory, ok := free["memory"]
		if !ok || freeMemory.Value() < 0 {
			traceLog.V(1).Info(
				"host with invalid memory capacity",
				"host", host, "freeMemory", freeMemory.String(),
			)
			delete(result.Activations, host) // host is already overloaded, remove from activations
			continue
		}
		// Calculate how many instances can fit on this host, based on memory.
		// Nova's memory_mb field is named "megabytes" in the API docs, but the values
		// are in mebibytes (1 MiB = 2^20 bytes). Multiply by 1<<20 to convert to bytes.
		//nolint:gosec // We're checking for underflows above (< 0).
		memorySlots := uint64(freeMemory.Value()) /
			(request.Flavor.MemoryMB * (1 << 20))
		if memorySlots < request.NumInstances {
			traceLog.V(1).Info(
				"filtering host due to insufficient RAM capacity",
				"host", host, "requested_mb", request.Flavor.MemoryMB,
				"available_mb", freeMemory.String(),
			)
			delete(result.Activations, host)
			continue
		}
		traceLog.V(1).Info(
			"host has enough capacity", "host", host,
			"requested_cpus", request.Flavor.VCPUs,
			"available_cpus", freeCPU.String(),
			"requested_memory_mb", request.Flavor.MemoryMB,
			"available_memory", freeMemory.String(),
		)
	}

	// Remove all hosts that weren't encountered.
	for host := range result.Activations {
		if _, ok := hostsEncountered[host]; !ok {
			delete(result.Activations, host)
			traceLog.V(1).Info(
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

// ownsInFlightHold reports whether the reservation is an in-flight hold the
// scheduling request owns itself (same VM) and may therefore skip, rather than
// count against itself. Nova serializes operations per VM, so a Nova lifecycle
// request for a VM may ignore that VM's in-flight holds — they belong to the
// same, now-superseded scheduling of this VM. Cortex-internal requests (e.g.
// failover scheduling, which also carries the VM uuid) are not serialized by
// Nova, so the VM's in-flight reservations still block for them.
func ownsInFlightHold(request compute.PlacementRequest, reservation v1alpha1.Reservation) bool {
	spec := reservation.Spec.InFlightReservation
	if reservation.Spec.Type != v1alpha1.ReservationTypeInFlight || spec == nil || spec.VMID != request.InstanceUUID {
		return false
	}
	// An unset intent means initial placement, which is a Nova lifecycle request.
	return request.Intent == "" || slices.Contains(api.NovaIntents, request.Intent)
}

// reservationKey uniquely identifies a reservation across namespaces, so a
// reservation can be matched against the skip set while iterating.
func reservationKey(res *v1alpha1.Reservation) string {
	return res.Namespace + "/" + res.Name
}

// inFlightHoldsToSkip returns the in-flight reservations whose block must NOT be
// counted on host hv, to avoid double-counting a landed VM (ADR 8, UC2).
//
// Once a VM lands on a host, that host's Status.Allocation already includes the
// VM, but the in-flight hold for the same VM/host (kept until the owning
// operation completes) would be subtracted again. For each VM present in
// hv.Status.Instances with Active=true, the smallest of that VM's in-flight
// holds on this host is skipped; all other holds (e.g. the larger target hold of
// a resize, or a sibling candidate on another host) are still counted in full.
//
// Only in-flight reservations targeting hv (via Spec.TargetHost or Status.Host)
// are considered. The result is keyed by reservationKey.
func inFlightHoldsToSkip(allReservations []v1alpha1.Reservation, hv hv1.Hypervisor) map[string]struct{} {
	activeVMs := make(map[string]struct{})
	for _, inst := range hv.Status.Instances {
		if inst.Active {
			activeVMs[inst.ID] = struct{}{}
		}
	}
	if len(activeVMs) == 0 {
		return nil
	}
	holdsByVM := make(map[string][]*v1alpha1.Reservation)
	for i := range allReservations {
		res := &allReservations[i]
		spec := res.Spec.InFlightReservation
		if res.Spec.Type != v1alpha1.ReservationTypeInFlight || spec == nil {
			continue
		}
		if res.Spec.TargetHost != hv.Name && res.Status.Host != hv.Name {
			continue
		}
		if _, active := activeVMs[spec.VMID]; !active {
			continue
		}
		holdsByVM[spec.VMID] = append(holdsByVM[spec.VMID], res)
	}
	skip := make(map[string]struct{}, len(holdsByVM))
	for _, holds := range holdsByVM {
		smallest := holds[0]
		for _, res := range holds[1:] {
			if inFlightHoldLess(res, smallest) {
				smallest = res
			}
		}
		skip[reservationKey(smallest)] = struct{}{}
	}
	return skip
}

// inFlightHoldLess orders in-flight holds by memory then CPU (ascending), with
// name as a stable final tiebreak so the skipped hold is deterministic.
func inFlightHoldLess(a, b *v1alpha1.Reservation) bool {
	am, bm := a.Spec.Resources[hv1.ResourceMemory], b.Spec.Resources[hv1.ResourceMemory]
	if c := am.Cmp(bm); c != 0 {
		return c < 0
	}
	ac, bc := a.Spec.Resources[hv1.ResourceCPU], b.Spec.Resources[hv1.ResourceCPU]
	if c := ac.Cmp(bc); c != 0 {
		return c < 0
	}
	return a.Name < b.Name
}
