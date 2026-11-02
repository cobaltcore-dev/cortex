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

// ReservationPlanner is the fourth pipeline stage. For a single failover
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
	// across passes rather than flapping from one host to another. description
	// is a human-readable label of what distinguishes the partition's hosts,
	// stored verbatim on each reservation. See characterizePartitions.
	PlanReservations(topo topology, partition []hv1.Hypervisor, description string) []v1alpha1.Reservation
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
// It builds a freshly computed reservation for every host in the partition and
// returns the first desiredCount of them, where desiredCount =
// max(len(partition)*ratio, minimum), ordered [already-reserved..., cheapest-new...]:
//
//   - Hosts that already hold a reservation for this partition come first, so a
//     host that is already a failover target stays one. This stability matters:
//     a designated failover host may have vms being live-migrated off it, and
//     flipping the designation to a different host each pass would waste that
//     work. If there are more such hosts than desiredCount, the surplus is left
//     out of the result and the reconciler prunes it, freeing the host back into
//     normal service.
//   - Not-yet-reserved hosts come next, ordered by ascending allocated capacity,
//     so when we must designate a fresh target we pick the host whose vms are
//     cheapest to migrate away.
//
// Crucially the spec is recomputed for every host, including already-reserved
// ones, rather than carried over from topo.existing. A kept reservation's
// SourceHosts, Resources and AvailabilityZone therefore always track the current
// partition: if the partition's membership or a host's capacity changed, the
// recomputed spec drifts from the stored one and the reconciler updates it.
func (p ratioReservationPlanner) PlanReservations(topo topology, partition []hv1.Hypervisor, description string) []v1alpha1.Reservation {
	// A partition of fewer than two hosts offers no failover: there is no peer
	// for a reserved host to receive an evacuation from. Reserving the lone host
	// would only block it from normal placement for no benefit, so skip it.
	if len(partition) < 2 {
		return nil
	}

	// Record which hosts already hold a failover reservation, so we can order
	// their recomputed reservations first and keep target hosts stable. We use
	// the spec's target host, not Status.Host: a freshly created reservation the
	// scheduler hasn't placed yet has an empty Status.Host.
	hostsReserved := []string{}
	for _, reservation := range topo.existing {
		hostsReserved = append(hostsReserved, reservation.Spec.TargetHost)
	}

	// Build a freshly computed reservation for every host in the partition and
	// split them into already-reserved and fresh candidates.
	existing := []v1alpha1.Reservation{}
	fresh := []v1alpha1.Reservation{}
	for _, hv := range partition {
		sourceHosts := []string{} // Other hosts in the same partition.
		for _, otherHv := range partition {
			if otherHv.Name != hv.Name {
				sourceHosts = append(sourceHosts, otherHv.Name)
			}
		}
		reservedResources := map[hv1.ResourceName]resource.Quantity{}
		maps.Copy(reservedResources, hv.Status.Capacity) // base capacity
		maps.Copy(reservedResources, hv.Status.EffectiveCapacity)
		reservation := v1alpha1.Reservation{
			ObjectMeta: metav1.ObjectMeta{
				Name: "failover-host-" + hv.Name,
				Labels: map[string]string{
					v1alpha1.LabelReservationType: v1alpha1.ReservationTypeLabelFailoverHost,
				},
			},
			Spec: v1alpha1.ReservationSpec{
				Type:             v1alpha1.ReservationTypeFailoverHost,
				SchedulingDomain: v1alpha1.SchedulingDomainNova,
				AvailabilityZone: hv.Labels[corev1.LabelTopologyZone],
				Resources:        reservedResources, // Allocate full host.
				TargetHost:       hv.Name,
				FailoverHostReservation: &v1alpha1.FailoverHostReservationSpec{
					SourceHosts:          sourceHosts,
					PartitionDescription: description,
				},
			},
		}
		if slices.Contains(hostsReserved, hv.Name) {
			existing = append(existing, reservation)
		} else {
			fresh = append(fresh, reservation)
		}
	}
	// topo.existing is a map, so sort for a stable order. Otherwise, when a
	// partition holds more already-reserved hosts than we need, the truncation
	// below would keep a different subset each pass and flap the target hosts.
	sort.SliceStable(existing, func(i, j int) bool {
		return existing[i].Spec.TargetHost < existing[j].Spec.TargetHost
	})
	// Order new candidates by ascending allocated capacity, so that if we have to
	// create reservations we pick the hosts that are cheapest to evacuate onto.
	sort.SliceStable(fresh, func(i, j int) bool {
		return cmpResources(fresh[i].Spec.Resources, fresh[j].Spec.Resources) < 0
	})

	// Candidate order is [already-reserved..., cheapest-new...]. Reserve a ratio
	// of the partition, never fewer than the minimum, but never so many that no
	// host is left to run workload — always keep at least one source host.
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
	// Round to GiB to absorb small per-node discrepancies in reported memory
	// capacity (Linux frees varying amounts during boot, so identical hardware
	// may show slightly different effective capacities).
	const gib = 1 << 30
	memA = *resource.NewQuantity(memA.Value()/gib*gib, resource.BinarySI)
	memB = *resource.NewQuantity(memB.Value()/gib*gib, resource.BinarySI)
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
