// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package reservations

import (
	hv1 "github.com/cobaltcore-dev/openstack-hypervisor-operator/api/v1"
	"k8s.io/apimachinery/pkg/api/resource"

	"github.com/cobaltcore-dev/cortex/api/v1alpha1"
)

// UnusedReservationCapacity returns the resources a Reservation should block on its host(s).
// This is the single source of truth used by both the capacity controller and
// filter_has_enough_capacity to ensure consistent accounting.
//
// For CommittedResourceReservations with allocations, confirmed VMs already appear in
// hv.Status.Allocation, so blocking the full slot would double-count them.
// The effective block is: max(slot − confirmedVMs, specOnlyVMs), clamped to zero.
// This adjustment is skipped when ignoreAllocations is true (empty-datacenter scenario,
// no VM deduction from capacity) or when the reservation is mid-migration
// (TargetHost != Status.Host) — in both cases the full slot is blocked on all hosts.
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
	}

	return res.Spec.Resources
}
