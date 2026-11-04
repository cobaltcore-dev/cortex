// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package filters

import (
	"github.com/cobaltcore-dev/cortex/api/v1alpha1"
	hv1 "github.com/cobaltcore-dev/openstack-hypervisor-operator/api/v1"
)

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
