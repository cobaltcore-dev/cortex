// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

// Package capacity holds the single source of truth for computing the usage and
// available capacity of a hypervisor including reservations. It is a self-contained
// helper over Reservation/Hypervisor CRDs with no dependency on the scheduling
// pipeline or the knowledge extractors, so it can be imported from below both the
// scheduling tree and the knowledge KPI tree without forming an import cycle.
//
// Three consumers share this math: the Nova capacity filter
// (filter_has_enough_capacity), the reservation controllers, and the KVM
// host-capacity KPI.
package capacity

import (
	"errors"
	"slices"

	hv1 "github.com/cobaltcore-dev/openstack-hypervisor-operator/api/v1"
	"k8s.io/apimachinery/pkg/api/resource"

	"github.com/cobaltcore-dev/cortex/api/v1alpha1"
)

// ErrNoCapacityData is returned by DescribeHostUsage when the hypervisor reports neither
// EffectiveCapacity nor Capacity — typically a host that has not been fully discovered
// yet. Callers should skip such a host.
var ErrNoCapacityData = errors.New("hypervisor has no capacity data")

// HostUsage is the per-resource usage accounting for a single hypervisor, including
// reservations. All maps are keyed by hv1.ResourceName.
type HostUsage struct {
	// Capacity is the starting capacity: Status.EffectiveCapacity (capacity ×
	// overcommit ratio), falling back to Status.Capacity when EffectiveCapacity is nil.
	Capacity map[hv1.ResourceName]resource.Quantity
	// Allocated is Status.Allocation (aggregate real-time usage of all running VMs).
	// Empty when Options.IgnoreAllocations is set.
	Allocated map[hv1.ResourceName]resource.Quantity
	// ReservedCommitted is the per-resource block from CommittedResourceReservations
	// (and any other non-failover reservation type that blocks), i.e. the slot capacity
	// not yet filled by confirmed VMs.
	ReservedCommitted map[hv1.ResourceName]resource.Quantity
	// ReservedFailover is the per-resource block from FailoverReservations.
	ReservedFailover map[hv1.ResourceName]resource.Quantity
	// Free is Capacity − Allocated − ReservedCommitted − ReservedFailover. Negative
	// values indicate over-subscription unless Options.ClampToZero floored them at 0.
	Free map[hv1.ResourceName]resource.Quantity
}

// Options tunes how DescribeHostUsage accounts for allocations and reservations.
type Options struct {
	// IgnoreAllocations skips subtracting Status.Allocation (and makes
	// UnusedReservationCapacity block the full slot). Used for empty-datacenter
	// capacity queries.
	IgnoreAllocations bool
	// IgnoredReservationTypes lists reservation types whose capacity is not blocked.
	IgnoredReservationTypes []v1alpha1.ReservationType
	// ClampToZero floors each Free entry at 0. Consumers that only care about
	// remaining headroom (the filter, the KPI) set this; consumers that need to
	// detect over-subscription (the reservation controllers) leave it false.
	ClampToZero bool
	// BlockUnplacedReservations, when false (the default), skips reservations whose
	// Spec.TargetHost is empty — ignoring evicted reservations whose Status.Host may
	// still lag. When true, a reservation blocks on Status.Host even with an empty
	// TargetHost (the stricter semantics used for fit checks).
	BlockUnplacedReservations bool
	// ExcludeReservation, when set, skips the reservation with this name+namespace —
	// e.g. to exclude the reservation being placed from its own fit check.
	ExcludeReservation *v1alpha1.Reservation
}

// DescribeHostUsage computes the usage breakdown for hv, subtracting allocation and
// the reservations that target this host. reservations may contain reservations for
// other hosts; only those targeting hv (via Spec.TargetHost or Status.Host == hv.Name)
// are counted. Returns ErrNoCapacityData when hv reports neither EffectiveCapacity nor
// Capacity.
func DescribeHostUsage(hv hv1.Hypervisor, reservations []v1alpha1.Reservation, opts Options) (HostUsage, error) {
	effCap := hv.Status.EffectiveCapacity
	if effCap == nil {
		effCap = hv.Status.Capacity
	}
	if effCap == nil {
		return HostUsage{}, ErrNoCapacityData
	}

	u := HostUsage{
		Capacity:          make(map[hv1.ResourceName]resource.Quantity, len(effCap)),
		Allocated:         make(map[hv1.ResourceName]resource.Quantity),
		ReservedCommitted: make(map[hv1.ResourceName]resource.Quantity),
		ReservedFailover:  make(map[hv1.ResourceName]resource.Quantity),
		Free:              make(map[hv1.ResourceName]resource.Quantity, len(effCap)),
	}
	for rn, qty := range effCap {
		u.Capacity[rn] = qty.DeepCopy()
		u.Free[rn] = qty.DeepCopy()
	}

	if !opts.IgnoreAllocations {
		for rn, allocated := range hv.Status.Allocation {
			u.Allocated[rn] = allocated.DeepCopy()
			if f, ok := u.Free[rn]; ok {
				f.Sub(allocated)
				u.Free[rn] = f
			}
		}
	}

	for i := range reservations {
		res := &reservations[i]
		if opts.ExcludeReservation != nil &&
			res.Name == opts.ExcludeReservation.Name &&
			res.Namespace == opts.ExcludeReservation.Namespace {
			continue
		}
		if slices.Contains(opts.IgnoredReservationTypes, res.Spec.Type) {
			continue
		}
		if !opts.BlockUnplacedReservations && res.Spec.TargetHost == "" {
			continue // evicted; status.Host may lag until reconcile
		}
		if res.Spec.TargetHost != hv.Name && res.Status.Host != hv.Name {
			continue
		}
		block := UnusedReservationCapacity(res, opts.IgnoreAllocations)
		bucket := u.ReservedCommitted
		if res.Spec.Type == v1alpha1.ReservationTypeFailover {
			bucket = u.ReservedFailover
		}
		for rn, qty := range block {
			if existing, ok := bucket[rn]; ok {
				existing.Add(qty)
				bucket[rn] = existing
			} else {
				bucket[rn] = qty.DeepCopy()
			}
			if f, ok := u.Free[rn]; ok {
				f.Sub(qty)
				u.Free[rn] = f
			}
		}
	}

	if opts.ClampToZero {
		zero := resource.MustParse("0")
		for rn, f := range u.Free {
			if f.Cmp(zero) < 0 {
				u.Free[rn] = zero.DeepCopy()
			}
		}
	}
	return u, nil
}

// Fits reports whether required fits within u.Free — i.e. every requested resource
// has at least that much free capacity remaining. A required resource absent from
// u.Free (the host does not report it) counts as not fitting.
func (u HostUsage) Fits(required map[hv1.ResourceName]resource.Quantity) bool {
	for rn, qty := range required {
		remaining, ok := u.Free[rn]
		if !ok {
			return false
		}
		if remaining.Cmp(qty) < 0 {
			return false
		}
	}
	return true
}

// UnusedReservationCapacity returns the resources a Reservation should block on its host(s).
// This is the single source of truth used by the capacity filter, the capacity controller,
// and the host-capacity KPI to ensure consistent accounting.
//
// CommittedResourceReservations: confirmed VMs already appear in hv.Status.Allocation,
// so blocking the full slot would double-count them. The remaining block is
// max(slot − sum(confirmed VM resources), 0) per resource. Skipped when ignoreAllocations
// is true or when mid-migration (TargetHost != Status.Host).
//
// FailoverReservations: always block the full Spec.Resources.
//
// FailoverHostReservations: block nothing. FailoverHostReservation is a new feature
// that reserves an entire host, and is intentionally not yet counted as blocked capacity
// by any consumer of this function (capacity filter, capacity controller, oversubscription
// controller). Returning an empty block here keeps all of them consistent in one place.
func UnusedReservationCapacity(res *v1alpha1.Reservation, ignoreAllocations bool) map[hv1.ResourceName]resource.Quantity {
	if res.Spec.Type == v1alpha1.ReservationTypeFailoverHost {
		// TODO: When failover reservations are in use, account for them.
		return nil
	}
	if res.Spec.Type == v1alpha1.ReservationTypeCommittedResource &&
		!ignoreAllocations &&
		res.Spec.TargetHost == res.Status.Host &&
		res.Status.CommittedResourceReservation != nil &&
		len(res.Status.CommittedResourceReservation.Allocation) > 0 {
		// Compute remaining slot capacity: slot - sum of confirmed VM resources.
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
	// No confirmed allocations, or ignoreAllocations, or mid-migration: block the full slot.
	return res.Spec.Resources
}
