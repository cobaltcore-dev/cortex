// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package reservations

import (
	hv1 "github.com/cobaltcore-dev/openstack-hypervisor-operator/api/v1"
	"k8s.io/apimachinery/pkg/api/resource"

	"github.com/cobaltcore-dev/cortex/api/v1alpha1"
)

// HostHasCapacityForReservation reports whether hv has sufficient remaining capacity to
// absorb res moving to it — i.e. whether the unfilled portion of res's slot fits alongside
// everything already committed on the host.
//
//  1. Start from EffectiveCapacity (or Capacity when EffectiveCapacity is nil).
//  2. Subtract hv.Status.Allocation (VMs physically running on this host).
//  3. For each other reservation assigned to this host (via Spec.TargetHost or Status.Host),
//     subtract its UnusedReservationCapacity.
//  4. Check that the remainder is ≥ UnusedReservationCapacity(res): the unfilled portion of
//     res's slot. Confirmed VMs in res already appear in hv.Status.Allocation (step 2), so
//     comparing against the full slot would count them twice.
//
// res itself is excluded from step 3 to avoid subtracting its own block from free capacity.
// Returns false when the hypervisor has no capacity data.
func HostHasCapacityForReservation(allReservations []v1alpha1.Reservation, hv hv1.Hypervisor, res *v1alpha1.Reservation) bool {
	// TODO consider refactor with HostFreeCapacity
	effCap := hv.Status.EffectiveCapacity
	if effCap == nil {
		effCap = hv.Status.Capacity
	}
	if effCap == nil {
		return false
	}

	free := make(map[hv1.ResourceName]resource.Quantity, len(effCap))
	for rn, qty := range effCap {
		free[rn] = qty.DeepCopy()
	}

	for rn, allocated := range hv.Status.Allocation {
		if f, ok := free[rn]; ok {
			f.Sub(allocated)
			free[rn] = f
		}
	}

	for i := range allReservations {
		other := &allReservations[i]
		if other.Name == res.Name && other.Namespace == res.Namespace {
			continue
		}
		// Only block resources from reservations that target or are confirmed on this host.
		targetsThisHost := other.Spec.TargetHost == hv.Name || other.Status.Host == hv.Name
		if !targetsThisHost {
			continue
		}
		for resourceName, block := range UnusedReservationCapacity(other, false) {
			if f, ok := free[resourceName]; ok {
				f.Sub(block)
				free[resourceName] = f
			}
		}
	}

	for resourceName, required := range UnusedReservationCapacity(res, false) {
		remaining, ok := free[resourceName]
		if !ok {
			return false
		}
		if remaining.Cmp(required) < 0 {
			return false
		}
	}
	return true
}

// HostFreeCapacity computes the remaining free capacity on hv after subtracting
// hv.Status.Allocation and UnusedReservationCapacity for all reservations on this host.
// Negative values indicate over-subscription for that resource.
// Returns nil when the hypervisor has no capacity data.
// Reservations not targeting this host (via Spec.TargetHost or Status.Host) are ignored.
func HostFreeCapacity(hostReservations []v1alpha1.Reservation, hv hv1.Hypervisor) map[hv1.ResourceName]resource.Quantity {
	// TODO consider refactor with HostHasCapacityForReservation
	effCap := hv.Status.EffectiveCapacity
	if effCap == nil {
		effCap = hv.Status.Capacity
	}
	if effCap == nil {
		return nil
	}

	free := make(map[hv1.ResourceName]resource.Quantity, len(effCap))
	for rn, qty := range effCap {
		free[rn] = qty.DeepCopy()
	}
	for rn, allocated := range hv.Status.Allocation {
		if f, ok := free[rn]; ok {
			f.Sub(allocated)
			free[rn] = f
		}
	}
	for i := range hostReservations {
		res := &hostReservations[i]
		if res.Spec.TargetHost == "" {
			continue // evicted/unplaced; status.Host may lag until reconcile
		}
		if res.Spec.TargetHost != hv.Name && res.Status.Host != hv.Name {
			continue
		}
		for rn, block := range UnusedReservationCapacity(res, false) {
			if f, ok := free[rn]; ok {
				f.Sub(block)
				free[rn] = f
			}
		}
	}
	return free
}

// UnusedReservationCapacity returns the resources a Reservation should block on its host(s).
// This is the single source of truth used by both the capacity controller and
// filter_has_enough_capacity to ensure consistent accounting.
//
// CommittedResourceReservations: confirmed VMs already appear in hv.Status.Allocation,
// so blocking the full slot would double-count them. The effective block is:
// max(slot − confirmedVMs, specOnlyVMs), clamped to zero. Skipped (full slot used) when
// ignoreAllocations is true or when mid-migration (TargetHost != Status.Host).
//
// FailoverReservations: always block the full Spec.Resources.
func UnusedReservationCapacity(res *v1alpha1.Reservation, ignoreAllocations bool) map[hv1.ResourceName]resource.Quantity {
	if res.Spec.Type == v1alpha1.ReservationTypeCommittedResource &&
		!ignoreAllocations &&
		res.Spec.TargetHost == res.Status.Host &&
		res.Spec.CommittedResourceReservation != nil &&
		len(res.Spec.CommittedResourceReservation.Allocations) > 0 {
		confirmedResources := make(map[hv1.ResourceName]resource.Quantity)
		specOnlyResources := make(map[hv1.ResourceName]resource.Quantity)

		statusAllocs := map[string]string{}
		if res.Status.CommittedResourceReservation != nil {
			statusAllocs = res.Status.CommittedResourceReservation.Allocations
		}

		for instanceUUID, allocation := range res.Spec.CommittedResourceReservation.Allocations {
			_, isConfirmed := statusAllocs[instanceUUID]
			for resourceName, quantity := range allocation.Resources {
				if isConfirmed {
					existing := confirmedResources[resourceName]
					existing.Add(quantity)
					confirmedResources[resourceName] = existing
				} else {
					existing := specOnlyResources[resourceName]
					existing.Add(quantity)
					specOnlyResources[resourceName] = existing
				}
			}
		}

		result := make(map[hv1.ResourceName]resource.Quantity)
		zero := resource.Quantity{}
		for resourceName, slotSize := range res.Spec.Resources {
			confirmed := confirmedResources[resourceName]
			specOnly := specOnlyResources[resourceName]

			remaining := slotSize.DeepCopy()
			remaining.Sub(confirmed)
			if remaining.Cmp(zero) < 0 {
				remaining = zero.DeepCopy()
			}

			if specOnly.Cmp(remaining) > 0 {
				result[resourceName] = specOnly.DeepCopy()
			} else {
				result[resourceName] = remaining
			}
		}
		return result
	} else {
		// FailoverReservations are always fully blocked and unused.
		return res.Spec.Resources
	}
}
