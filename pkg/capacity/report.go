// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

// Package capacity computes a point-in-time breakdown of a single KVM hypervisor's
// capacity from the hypervisor resource and the reservations placed on it.
//
// It answers, in one type, how much of a host's capacity is used by running workloads,
// reserved by committed reservations, held back for failover, how much remains
// available, and by how much the host is over-subscribed.
package capacity

import (
	hv1 "github.com/cobaltcore-dev/openstack-hypervisor-operator/api/v1"
	"k8s.io/apimachinery/pkg/api/resource"

	"github.com/cobaltcore-dev/cortex/api/v1alpha1"
)

// HostUsageReport is a point-in-time breakdown of one host's capacity. Every map is
// keyed by hv1.ResourceName and keeps the source quantities' formats (cpu DecimalSI
// cores, memory BinarySI bytes). Build one with NewHostUsageReport.
type HostUsageReport struct {
	// Total is the schedulable capacity per resource: the effective
	// (overcommit-adjusted) capacity, falling back to the raw capacity per resource.
	Total map[hv1.ResourceName]resource.Quantity

	// Used is capacity consumed by the workloads the hypervisor currently reports
	// running (its allocation): the committed view, i.e. the sum of workload sizes, not
	// live utilisation.
	Used map[hv1.ResourceName]resource.Quantity

	// Reserved is capacity blocked by committed-resource reservations that is not yet
	// backed by a running workload (the unconfirmed remainder of each slot; confirmed
	// workloads already count toward Used, so only the remainder is counted here to
	// avoid double-counting).
	Reserved map[hv1.ResourceName]resource.Quantity

	// Failover is capacity held back for HA/failover reservations. It blocks placements
	// exactly like Reserved and is subtracted from Available too; it is surfaced as its
	// own field only so a consumer can report the failover budget separately from
	// committed-resource reservations.
	Failover map[hv1.ResourceName]resource.Quantity

	// Available is the capacity free for a new placement: Total - Used - Reserved -
	// Failover, clamped at zero per resource. Any shortfall is reported in Oversubscribed.
	Available map[hv1.ResourceName]resource.Quantity

	// Oversubscribed is, per resource, how far Used + Reserved + Failover exceeds Total
	// (zero when the host is within capacity).
	Oversubscribed map[hv1.ResourceName]resource.Quantity
}

// NewHostUsageReport computes the report for hv from the reservations placed on it. A
// reservation counts when its Spec.TargetHost or Status.Host equals hv.Name; a
// reservation whose Spec.TargetHost is empty is treated as unplaced (e.g. evicted,
// pending status cleanup) and never counts, even if Status.Host still points here. Each
// counted reservation contributes ReservationBlock worth of capacity — failover
// reservations to Failover, committed-resource slots to Reserved.
//
// Total is hv.Status.EffectiveCapacity per resource, falling back to hv.Status.Capacity
// for any resource the effective map reports as zero or omits; Used is
// hv.Status.Allocation. A resource that is zero or absent in both capacity maps is
// dropped. When no resource has non-zero capacity the zero report (nil maps) is returned.
//
// ignoreAllocations, when true, treats every committed-resource slot as fully blocked
// regardless of confirmed workloads and leaves Used empty — the empty-host view used by
// capacity probing.
func NewHostUsageReport(hv hv1.Hypervisor, reservations []v1alpha1.Reservation, ignoreAllocations bool) HostUsageReport {
	if hv.Status.EffectiveCapacity == nil && hv.Status.Capacity == nil {
		return HostUsageReport{}
	}

	rep := HostUsageReport{
		Total:          make(map[hv1.ResourceName]resource.Quantity),
		Used:           make(map[hv1.ResourceName]resource.Quantity),
		Reserved:       make(map[hv1.ResourceName]resource.Quantity),
		Failover:       make(map[hv1.ResourceName]resource.Quantity),
		Available:      make(map[hv1.ResourceName]resource.Quantity),
		Oversubscribed: make(map[hv1.ResourceName]resource.Quantity),
	}
	// Total is the effective capacity per resource, falling back to the raw capacity for
	// any resource the operator reports as zero or omits in the effective map. A resource
	// that is zero or absent in both is dropped entirely.
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
		rep.Reserved[name] = resource.Quantity{}
		rep.Failover[name] = resource.Quantity{}
	}
	if len(rep.Total) == 0 {
		return HostUsageReport{}
	}
	if !ignoreAllocations {
		for name, qty := range hv.Status.Allocation {
			if _, ok := rep.Used[name]; ok {
				rep.Used[name] = qty.DeepCopy()
			}
		}
	}

	for i := range reservations {
		res := reservations[i]
		if res.Spec.TargetHost == "" {
			continue // unplaced/evicted; status may lag until the next reconcile
		}
		if res.Spec.TargetHost != hv.Name && res.Status.Host != hv.Name {
			continue
		}
		target := rep.Reserved
		if res.Spec.Type == v1alpha1.ReservationTypeFailover || res.Spec.Type == v1alpha1.ReservationTypeFailoverHost {
			target = rep.Failover
		}
		for name, qty := range ReservationBlock(res, ignoreAllocations) {
			if _, ok := target[name]; !ok {
				continue // host does not advertise this resource
			}
			sum := target[name]
			sum.Add(qty)
			target[name] = sum
		}
	}

	for name, totalQty := range rep.Total {
		avail := totalQty.DeepCopy()
		avail.Sub(rep.Used[name])
		avail.Sub(rep.Reserved[name])
		avail.Sub(rep.Failover[name])
		if avail.Sign() < 0 {
			over := avail.DeepCopy()
			over.Neg()
			rep.Oversubscribed[name] = over
			rep.Available[name] = resource.Quantity{}
		} else {
			rep.Oversubscribed[name] = resource.Quantity{}
			rep.Available[name] = avail
		}
	}
	return rep
}

// Fits reports whether demand fits into Available: for every resource the demand asks
// for, Available must hold at least demand[res]. Pass the total demand across all
// instances (already scaled by the caller) — this method does no scaling of its own. A
// demand entry that is absent or non-positive imposes no constraint; a demanded resource
// missing from Available (or present but insufficient) fails.
func (r HostUsageReport) Fits(demand map[hv1.ResourceName]resource.Quantity) bool {
	for name, need := range demand {
		if need.Sign() <= 0 {
			continue
		}
		have, ok := r.Available[name]
		if !ok || have.Cmp(need) < 0 {
			return false
		}
	}
	return true
}

// ReservationBlock returns the resources a single reservation blocks on its host. It is
// the shared rule every caller uses so reservations are accounted identically.
//
// A committed-resource reservation whose confirmed workloads already appear in the
// hypervisor allocation blocks only the unconfirmed remainder of its slot, i.e.
// max(slot − sum(confirmed workloads), 0) per resource; this avoids double-counting
// those workloads. It blocks the full slot instead when ignoreAllocations is set or when
// mid-migration (TargetHost != Status.Host).
//
// A failover reservation blocks its full Spec.Resources.
//
// A failover-host reservation blocks nothing: it reserves an entire host and is
// intentionally not yet counted as blocked capacity.
func ReservationBlock(res v1alpha1.Reservation, ignoreAllocations bool) map[hv1.ResourceName]resource.Quantity {
	if res.Spec.Type == v1alpha1.ReservationTypeFailoverHost {
		// TODO: When failover host reservations are in use, account for them.
		return nil
	}
	if res.Spec.Type == v1alpha1.ReservationTypeCommittedResource &&
		!ignoreAllocations &&
		res.Spec.TargetHost == res.Status.Host &&
		res.Status.CommittedResourceReservation != nil &&
		len(res.Status.CommittedResourceReservation.Allocation) > 0 {
		result := make(map[hv1.ResourceName]resource.Quantity, len(res.Spec.Resources))
		zero := resource.MustParse("0")
		for rn, slotQty := range res.Spec.Resources {
			remaining := slotQty.DeepCopy()
			for _, alloc := range res.Status.CommittedResourceReservation.Allocation {
				if used, ok := alloc.Resources[rn]; ok {
					remaining.Sub(used)
				}
			}
			if remaining.Cmp(zero) < 0 {
				remaining = zero.DeepCopy()
			}
			result[rn] = remaining
		}
		return result
	}
	return res.Spec.Resources
}
