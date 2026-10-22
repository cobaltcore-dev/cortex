// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package failoverhost

import (
	"maps"
	"slices"
	"sort"

	"github.com/cobaltcore-dev/cortex/api/v1alpha1"
	hv1 "github.com/cobaltcore-dev/openstack-hypervisor-operator/api/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// ReservationPlanner is the third pipeline stage. For a single failover
// partition it decides which hosts should become failover targets, i.e. be kept
// empty so another host in the partition can gang-evacuate onto them.
//
// We do not reserve a fixed number per partition. Reserving too few leaves no
// landing space when a host fails; reserving too many wastes capacity. So the
// count scales with the partition: a configured ratio of the hosts, floored at a
// configured minimum so even small partitions keep some headroom.
type ReservationPlanner interface {
	// PlanReservations returns the desired failover host reservations for the
	// given partition. The topology supplies the reservations that already
	// exist, which the planner prefers to keep so target hosts stay stable
	// across passes rather than flapping from one host to another.
	PlanReservations(topo topology, partition []hv1.Hypervisor) []v1alpha1.Reservation
}

// ratioReservationPlanner reserves a ratio of each partition's hosts as failover
// targets, never fewer than a configured minimum. It prefers to keep existing
// reservations and, when it must create new ones, picks the hosts with the
// least allocated capacity so that the eventual evacuation migrates as few
// resources as possible.
type ratioReservationPlanner struct {
	// ratio is the fraction of a partition's hosts to reserve as failover
	// targets, rounded down (e.g. 0.1 of a 10-host partition reserves 1).
	ratio float64
	// minimum is the floor on reservations per partition, applied even when the
	// ratio would round down to fewer.
	minimum int
}

// PlanReservations implements ReservationPlanner.
//
// It builds an ordered candidate list [existing..., cheapest-new...] and returns
// the first desiredCount entries, where desiredCount = max(len(partition)*ratio,
// minimum):
//
//   - Existing reservations for hosts in this partition come first, so a host
//     that is already a failover target stays one. This stability matters: a
//     designated failover host may have vms being live-migrated off it, and
//     flipping the designation to a different host each pass would waste that
//     work. If there are more existing reservations than desiredCount, the
//     surplus is simply left out of the result and the reconciler prunes it,
//     freeing the host back into normal service.
//   - New reservations (for not-yet-reserved hosts) come next, ordered by
//     ascending allocated capacity, so when we must designate a fresh target we
//     pick the host whose vms are cheapest to migrate away.
func (p ratioReservationPlanner) PlanReservations(topo topology, partition []hv1.Hypervisor) []v1alpha1.Reservation {
	// A partition of fewer than two hosts offers no failover: there is no peer
	// for a reserved host to receive an evacuation from. Reserving the lone host
	// would only block it from normal placement for no benefit, so skip it.
	if len(partition) < 2 {
		return nil
	}

	// Existing reservations whose target host is in this partition come first,
	// so already-reserved hosts win and target hosts don't flip between passes.
	// We also record every already-reserved host so we don't propose a duplicate.
	existing := []v1alpha1.Reservation{}
	hostsReserved := []string{}
	for _, reservation := range topo.existing {
		// Use the spec's target host, not Status.Host: a freshly created
		// reservation the scheduler hasn't placed yet has an empty Status.Host,
		// which would let us create a duplicate reservation for the same host.
		hostsReserved = append(hostsReserved, reservation.Spec.TargetHost)
		for _, hv := range partition {
			if reservation.Spec.TargetHost == hv.Name {
				existing = append(existing, reservation)
				break
			}
		}
	}
	// topo.existing is a map, so sort for a stable order. Otherwise, when a
	// partition holds more existing reservations than we need, the truncation
	// below would keep a different subset each pass and flap the target hosts.
	sort.SliceStable(existing, func(i, j int) bool {
		return existing[i].Spec.TargetHost < existing[j].Spec.TargetHost
	})

	// Build candidate new reservations for every not-yet-reserved host.
	fresh := []v1alpha1.Reservation{}
	for _, hv := range partition {
		if slices.Contains(hostsReserved, hv.Name) {
			continue
		}
		sourceHosts := []string{} // Other hosts in the same partition.
		for _, otherHv := range partition {
			if otherHv.Name != hv.Name {
				sourceHosts = append(sourceHosts, otherHv.Name)
			}
		}
		reservedResources := map[hv1.ResourceName]resource.Quantity{}
		maps.Copy(reservedResources, hv.Status.Capacity) // base capacity
		maps.Copy(reservedResources, hv.Status.EffectiveCapacity)
		fresh = append(fresh, v1alpha1.Reservation{
			ObjectMeta: metav1.ObjectMeta{
				Name: "failover-host-" + hv.Name,
				Labels: map[string]string{
					v1alpha1.LabelReservationType: v1alpha1.ReservationTypeLabelFailoverHost,
				},
			},
			Spec: v1alpha1.ReservationSpec{
				Type:                    v1alpha1.ReservationTypeFailoverHost,
				SchedulingDomain:        v1alpha1.SchedulingDomainNova,
				AvailabilityZone:        hv.Labels[corev1.LabelTopologyZone],
				Resources:               reservedResources, // Allocate full host.
				TargetHost:              hv.Name,
				FailoverHostReservation: &v1alpha1.FailoverHostReservationSpec{SourceHosts: sourceHosts},
			},
		})
	}
	// Order new candidates by ascending allocated capacity, so that if we have to
	// create reservations we pick the hosts that are cheapest to evacuate onto.
	sort.SliceStable(fresh, func(i, j int) bool {
		return cmpResources(fresh[i].Spec.Resources, fresh[j].Spec.Resources) < 0
	})

	// Candidate order is [existing..., cheapest-new...]. Reserve a ratio of the
	// partition, never fewer than the minimum, but never so many that no host is
	// left to run workload — always keep at least one source host.
	available := make([]v1alpha1.Reservation, 0, len(existing)+len(fresh))
	available = append(available, existing...)
	available = append(available, fresh...)
	desiredCount := int(float64(len(partition)) * p.ratio)
	if desiredCount < p.minimum {
		desiredCount = p.minimum
	}
	if maxReservable := len(partition) - 1; desiredCount > maxReservable {
		desiredCount = maxReservable
	}
	if len(available) > desiredCount {
		available = available[:desiredCount]
	}
	return available
}

// cmpResources compares the resources of hypervisor a with the resources
// of hypervisor b. If the resources of a are greater then b, it returns 1.
// If the resources of a are less than b, it returns -1.
// If the resources are equal, it returns 0.
func cmpResources(a, b map[hv1.ResourceName]resource.Quantity) int {
	// Consider memory first, then consider cpu if there isn't already
	// a decisive comparison can be made based on memory.
	memA, ok := a[hv1.ResourceMemory]
	if !ok {
		memA = *resource.NewQuantity(0, resource.BinarySI)
	}
	memB, ok := b[hv1.ResourceMemory]
	if !ok {
		memB = *resource.NewQuantity(0, resource.BinarySI)
	}
	if memA.Cmp(memB) > 0 {
		return 1
	} else if memA.Cmp(memB) < 0 {
		return -1
	}
	cpuA, ok := a[hv1.ResourceCPU]
	if !ok {
		cpuA = *resource.NewQuantity(0, resource.DecimalSI)
	}
	cpuB, ok := b[hv1.ResourceCPU]
	if !ok {
		cpuB = *resource.NewQuantity(0, resource.DecimalSI)
	}
	if cpuA.Cmp(cpuB) > 0 {
		return 1
	} else if cpuA.Cmp(cpuB) < 0 {
		return -1
	}
	return 0
}
