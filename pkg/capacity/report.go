// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

// Package capacity computes a point-in-time breakdown of a single KVM hypervisor's
// capacity from the hypervisor resource and the reservations placed on it.
//
// It separates two concerns:
//
//   - Per-host arithmetic (HostUsageReport): mechanical. Given a host's capacity, its
//     running workloads and the capacity each reservation blocks, it produces
//     Total/Used/Reserved/Available/Oversubscribed. Available subtracts every reservation
//     the caller passed in; which reservations count is decided before the report is built.
//
//   - Per-reservation blocking policy (ReservationEffectivelyBlockedCapacity): the one
//     reservation-type- and lifecycle-aware rule, kept here so every caller accounts
//     reservations identically, but called out explicitly rather than hidden in the
//     report builder.
//
// Reserved is keyed by reservation type so no single type (failover) gets a privileged
// field; a consumer sums the types it cares about with ReservedTotal.
package capacity

import (
	"slices"

	hv1 "github.com/cobaltcore-dev/openstack-hypervisor-operator/api/v1"
	"k8s.io/apimachinery/pkg/api/resource"

	"github.com/cobaltcore-dev/cortex/api/v1alpha1"
)

// ResourceMap is a per-resource quantity map keyed by hv1 resource name (cpu DecimalSI
// cores, memory BinarySI bytes). It is the unit every field and helper in this package
// speaks in.
type ResourceMap = map[hv1.ResourceName]resource.Quantity

// HostUsageReport is a point-in-time, purely arithmetic breakdown of one host's capacity.
// Build one with NewHostUsageReport. It carries no placement policy: Available subtracts
// every reservation the caller passed in; callers decide for themselves whether a demand
// fits (against Available, or against Total for an empty-datacenter view).
type HostUsageReport struct {
	// Total is the schedulable capacity per resource: the effective (overcommit-adjusted)
	// capacity, falling back to the raw capacity for any resource the effective map
	// reports as zero or omits. A resource absent here is unknown to this host.
	Total ResourceMap

	// Used is capacity consumed by the workloads the hypervisor currently reports running
	// (its allocation): the committed view (sum of workload sizes), not live utilisation.
	Used ResourceMap

	// Reserved is capacity blocked by reservations not yet backed by a running workload,
	// keyed first by reservation type then by resource. Each type's inner map is the sum
	// of ReservationEffectivelyBlockedCapacity over that type's counted reservations on
	// this host. Consumers choose which types to account for and sum them with
	// ReservedTotal; every type is subtracted from Available.
	Reserved map[v1alpha1.ReservationType]ResourceMap

	// Available is capacity free for a new placement: Total − Used − ReservedTotal(all),
	// clamped at zero per resource. Any shortfall surfaces in Oversubscribed.
	Available ResourceMap

	// Oversubscribed is, per resource, how far Used + ReservedTotal(all) exceeds Total
	// (absent/zero when the host is within capacity).
	Oversubscribed ResourceMap
}

// ReservedTotal sums Reserved across the given reservation types (all types when none are
// passed) into one per-resource map, keeping the source quantities' formats. Use it for
// the combined blocked capacity rather than the per-type breakdown.
func (r HostUsageReport) ReservedTotal(types ...v1alpha1.ReservationType) ResourceMap {
	total := ResourceMap{}
	for resType, byRes := range r.Reserved {
		if len(types) > 0 && !slices.Contains(types, resType) {
			continue
		}
		for name, qty := range byRes {
			sum := total[name]
			sum.Add(qty)
			total[name] = sum
		}
	}
	return total
}

// NewHostUsageReport computes the arithmetic report for hv from the reservations the
// caller has already decided should count. It always folds honestly:
// Available = Total − Used − Reserved(all types).
//
// A reservation counts toward a host when its Spec.TargetHost or Status.Host equals
// hv.Name; one whose Spec.TargetHost is empty is treated as unplaced and never counts.
// Total is EffectiveCapacity, falling back per resource to raw Capacity for any resource
// the operator reports as zero or omits; a resource zero in both is dropped. When no
// resource has capacity the zero report is returned.
//
// For an "empty host" view (what a host could hold once fully cleared), the caller passes
// no reservations and reads Total: with no workloads subtracted that is exactly the free
// capacity. There is no flag for it — a flag that still counted reservations would be a
// half-truth.
func NewHostUsageReport(hv hv1.Hypervisor, reservations []v1alpha1.Reservation) HostUsageReport {
	rep := HostUsageReport{
		Total:          ResourceMap{},
		Used:           ResourceMap{},
		Reserved:       map[v1alpha1.ReservationType]ResourceMap{},
		Available:      ResourceMap{},
		Oversubscribed: ResourceMap{},
	}
	// Total is the effective (overcommit-adjusted) capacity per resource, falling back to
	// the raw capacity for any resource the operator reports as zero or omits in the
	// effective map. A resource zero or absent in both is dropped entirely.
	for name := range hv.Status.EffectiveCapacity {
		rep.Total[name] = resource.Quantity{}
	}
	for name := range hv.Status.Capacity {
		rep.Total[name] = resource.Quantity{}
	}
	for name := range rep.Total {
		qty := hv.Status.EffectiveCapacity[name]
		if qty.IsZero() {
			qty = hv.Status.Capacity[name]
		}
		if qty.IsZero() {
			delete(rep.Total, name)
			continue
		}
		rep.Total[name] = qty.DeepCopy()
		rep.Used[name] = resource.Quantity{}
	}
	if len(rep.Total) == 0 {
		return HostUsageReport{}
	}
	for name, qty := range hv.Status.Allocation {
		if _, ok := rep.Total[name]; ok {
			rep.Used[name] = qty.DeepCopy()
		}
	}

	skipHold := redundantInFlightHolds(reservations, hv)
	for i := range reservations {
		res := reservations[i]
		if res.Spec.TargetHost == "" {
			continue // unplaced/evicted; status may lag until the next reconcile
		}
		if res.Spec.TargetHost != hv.Name && res.Status.Host != hv.Name {
			continue
		}
		if skipHold[i] {
			continue
		}
		byRes := rep.Reserved[res.Spec.Type]
		if byRes == nil {
			byRes = ResourceMap{}
			rep.Reserved[res.Spec.Type] = byRes
		}
		for name, qty := range ReservationEffectivelyBlockedCapacity(res) {
			if _, ok := rep.Total[name]; !ok {
				continue // host does not advertise this resource
			}
			sum := byRes[name]
			sum.Add(qty)
			byRes[name] = sum
		}
	}

	reservedTotal := rep.ReservedTotal()
	for name, totalQty := range rep.Total {
		avail := totalQty.DeepCopy()
		avail.Sub(rep.Used[name])
		avail.Sub(reservedTotal[name])
		if avail.Sign() < 0 {
			over := avail.DeepCopy()
			over.Neg()
			rep.Oversubscribed[name] = over
			rep.Available[name] = resource.Quantity{}
		} else {
			rep.Available[name] = avail
		}
	}
	return rep
}

// redundantInFlightHolds returns, as a set of indices into reservations, the in-flight
// holds on hv that must not be counted because the host already accounts for them.
//
// Once a VM lands on a host, hv.Status.Allocation reflects it at its current size, so
// exactly one of its in-flight holds there double-counts it. For each VM active on hv,
// its smallest in-flight hold (the current size already in Allocation) is marked. Any
// larger hold — e.g. the target slot of a same-host resize — is left counted at its FULL
// size, not just the delta: that conservatively over-reserves by the current size
// (counted in both Used and the kept hold) for the one reconcile cycle until the hold
// releases, which keeps the Used+Reserved+Available stack summing to Total while erring
// toward reserving too much rather than too little.
//
// Only in-flight holds targeting hv (Spec.TargetHost or Status.Host) and belonging to a
// VM active on hv count. Holds are ordered by memory, then CPU, with name as a stable
// tiebreak, so the marked hold is deterministic.
func redundantInFlightHolds(reservations []v1alpha1.Reservation, hv hv1.Hypervisor) map[int]bool {
	activeOnHost := make(map[string]bool)
	for _, inst := range hv.Status.Instances {
		if inst.Active {
			activeOnHost[inst.ID] = true
		}
	}
	smallestPerVM := make(map[string]int) // VM uuid -> index of its smallest in-flight hold on hv
	for i := range reservations {
		res := &reservations[i]
		spec := res.Spec.InFlightReservation
		if res.Spec.Type != v1alpha1.ReservationTypeInFlight || spec == nil || !activeOnHost[spec.VMID] {
			continue
		}
		if res.Spec.TargetHost != hv.Name && res.Status.Host != hv.Name {
			continue
		}
		if cur, seen := smallestPerVM[spec.VMID]; !seen || inFlightHoldSmaller(res, &reservations[cur]) {
			smallestPerVM[spec.VMID] = i
		}
	}
	skip := make(map[int]bool, len(smallestPerVM))
	for _, i := range smallestPerVM {
		skip[i] = true
	}
	return skip
}

// inFlightHoldSmaller orders two in-flight holds by memory, then CPU, with name as a
// stable final tiebreak, so the smallest hold a landed VM holds on a host — the one
// already reflected in hv.Status.Allocation — is picked deterministically.
func inFlightHoldSmaller(a, b *v1alpha1.Reservation) bool {
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

// ReservationEffectivelyBlockedCapacity returns, per resource, the capacity a single
// reservation effectively blocks on its host right now: its reserved slot minus the
// workloads already running against it.
//
// Per reservation type:
//   - CommittedResource, placed and confirmed (TargetHost == Status.Host, allocation
//     present): the unconfirmed remainder of its slot, max(slot − confirmed workloads, 0)
//     per resource, so workloads already in the hypervisor allocation are not
//     double-counted. The full slot while mid-migration or before any allocation.
//   - Failover / InFlight: the full Spec.Resources (nothing is allocated against them).
//     When a VM has already landed on the host, NewHostUsageReport skips that VM's
//     smallest in-flight hold here (its current size is in hv.Status.Allocation); that
//     per-host netting needs the host's live instance list, so it is done by the caller.
//   - FailoverHost: the planner writes the full host capacity into Spec.Resources (it
//     reserves an entire host), and this blocks that whole capacity regardless of
//     lifecycle stage. New placements onto a draining failover host are kept off by
//     filter_failover_host; this function only accounts the reserved capacity.
func ReservationEffectivelyBlockedCapacity(res v1alpha1.Reservation) ResourceMap {
	result := make(ResourceMap, len(res.Spec.Resources))
	for name, qty := range res.Spec.Resources {
		result[name] = qty.DeepCopy()
	}

	switch res.Spec.Type {
	// CommittedResources track how much of the reserved slot is already consumed by workloads on the host.
	case v1alpha1.ReservationTypeCommittedResource:
		if res.Spec.TargetHost != res.Status.Host || res.Status.CommittedResourceReservation == nil {
			return result
		}
		for rn, slotQty := range res.Spec.Resources {
			remaining := slotQty.DeepCopy()
			for _, alloc := range res.Status.CommittedResourceReservation.Allocation {
				if used, ok := alloc.Resources[rn]; ok {
					remaining.Sub(used)
				}
			}
			if remaining.Sign() < 0 {
				remaining = resource.Quantity{}
			}
			result[rn] = remaining
		}
		return result
	// Failover and InFlight reservations block their full Spec.Resources. When a VM has
	// landed on the host, NewHostUsageReport drops that VM's smallest in-flight hold
	// here (its current size is in hv.Status.Allocation); that per-host netting needs
	// the hypervisor's live instance list, which this per-reservation helper does not have.
	case v1alpha1.ReservationTypeFailover, v1alpha1.ReservationTypeInFlight, v1alpha1.ReservationTypeFailoverHost:
		return result
	default:
		return result
	}
}
