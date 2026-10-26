// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package failoverhost

import (
	"slices"
	"testing"

	"github.com/cobaltcore-dev/cortex/api/v1alpha1"
	hv1 "github.com/cobaltcore-dev/openstack-hypervisor-operator/api/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// res builds a resource map from an optional memory and cpu quantity string.
func res(mem, cpu string) map[hv1.ResourceName]resource.Quantity {
	out := map[hv1.ResourceName]resource.Quantity{}
	if mem != "" {
		out[hv1.ResourceMemory] = resource.MustParse(mem)
	}
	if cpu != "" {
		out[hv1.ResourceCPU] = resource.MustParse(cpu)
	}
	return out
}

func TestCmpResources(t *testing.T) {
	tests := []struct {
		name string
		a, b map[hv1.ResourceName]resource.Quantity
		want int
	}{
		{"equal", res("8Gi", "4"), res("8Gi", "4"), 0},
		{"memory greater dominates", res("16Gi", "1"), res("8Gi", "99"), 1},
		{"memory less dominates", res("4Gi", "99"), res("8Gi", "1"), -1},
		{"cpu breaks memory tie greater", res("8Gi", "8"), res("8Gi", "4"), 1},
		{"cpu breaks memory tie less", res("8Gi", "2"), res("8Gi", "4"), -1},
		{"missing memory treated as zero", res("", "4"), res("8Gi", "4"), -1},
		{"both empty are equal", res("", ""), res("", ""), 0},
		{"missing cpu treated as zero", res("8Gi", ""), res("8Gi", "4"), -1},
		// Sub-GiB discrepancies (e.g. from different amounts of memory freed during
		// Linux boot) must not be treated as different capacities.
		{"sub-gib difference treated as equal", res("4227288964Ki", "4"), res("4227172876Ki", "4"), 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := cmpResources(tt.a, tt.b); got != tt.want {
				t.Errorf("cmpResources() = %d, want %d", got, tt.want)
			}
		})
	}
}

// partitionOf builds a partition of hosts, each with the given memory capacity
// in GiB so the planner can order them by allocation cost.
func partitionOf(memByHost map[string]int64) []hv1.Hypervisor {
	partition := make([]hv1.Hypervisor, 0, len(memByHost))
	for name, memGi := range memByHost {
		partition = append(partition, hv1.Hypervisor{
			ObjectMeta: metav1.ObjectMeta{
				Name:   name,
				Labels: map[string]string{corev1.LabelTopologyZone: "az1"},
			},
			Status: hv1.HypervisorStatus{
				Capacity: map[hv1.ResourceName]resource.Quantity{
					hv1.ResourceMemory: *resource.NewQuantity(memGi*1024*1024*1024, resource.BinarySI),
				},
			},
		})
	}
	slices.SortFunc(partition, func(a, b hv1.Hypervisor) int { return cmpStrings(a.Name, b.Name) })
	return partition
}

func cmpStrings(a, b string) int {
	switch {
	case a < b:
		return -1
	case a > b:
		return 1
	default:
		return 0
	}
}

func TestPlanReservationsRatioAndMinimum(t *testing.T) {
	partition := partitionOf(map[string]int64{"hv1": 64, "hv2": 32, "hv3": 16, "hv4": 48})
	empty := topology{existing: map[corev1.ObjectReference]v1alpha1.Reservation{}}

	tests := []struct {
		name    string
		ratio   float64
		minimum int
		want    int // number of planned reservations
	}{
		{"ratio rounds down", 0.5, 1, 2},    // 4 * 0.5 = 2
		{"minimum floors ratio", 0.1, 2, 2}, // 4 * 0.1 = 0, floored to 2
		{"ratio above minimum wins", 0.75, 1, 3},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			planner := ratioReservationPlanner{ratio: tt.ratio, minimum: tt.minimum}
			got := planner.PlanReservations(empty, partition, "")
			if len(got) != tt.want {
				t.Fatalf("planned %d reservations, want %d", len(got), tt.want)
			}
			for _, r := range got {
				if r.Spec.Type != v1alpha1.ReservationTypeFailoverHost {
					t.Errorf("unexpected reservation type %q", r.Spec.Type)
				}
				if r.Spec.AvailabilityZone != "az1" {
					t.Errorf("availability zone = %q, want az1", r.Spec.AvailabilityZone)
				}
			}
		})
	}
}

func TestPlanReservationsPrefersSmallestHosts(t *testing.T) {
	// With no existing reservations, the planner creates new ones for the hosts
	// that are cheapest to evacuate, i.e. the smallest capacity first.
	partition := partitionOf(map[string]int64{"big": 64, "small": 16, "medium": 32})
	empty := topology{existing: map[corev1.ObjectReference]v1alpha1.Reservation{}}
	planner := ratioReservationPlanner{ratio: 0.34, minimum: 1} // 3 * 0.34 = 1

	got := planner.PlanReservations(empty, partition, "")
	if len(got) != 1 {
		t.Fatalf("planned %d reservations, want 1", len(got))
	}
	if got[0].Spec.TargetHost != "small" {
		t.Errorf("target host = %q, want smallest host 'small'", got[0].Spec.TargetHost)
	}
}

func TestPlanReservationsKeepsExistingStable(t *testing.T) {
	partition := partitionOf(map[string]int64{"big": 64, "small": 16})
	existing := v1alpha1.Reservation{
		ObjectMeta: metav1.ObjectMeta{Name: "failover-host-big"},
		Spec: v1alpha1.ReservationSpec{
			Type:       v1alpha1.ReservationTypeFailoverHost,
			TargetHost: "big", // already reserved, even though it's the bigger host
			Resources:  res("64Gi", ""),
		},
	}
	topo := topology{existing: map[corev1.ObjectReference]v1alpha1.Reservation{
		{Name: existing.Name}: existing,
	}}
	planner := ratioReservationPlanner{ratio: 0.5, minimum: 1} // reserve 1 of 2

	got := planner.PlanReservations(topo, partition, "")
	if len(got) != 1 {
		t.Fatalf("planned %d reservations, want 1", len(got))
	}
	// The existing reservation wins over creating a new one on the smaller host,
	// so target hosts don't flip between passes.
	if got[0].Spec.TargetHost != "big" {
		t.Errorf("target host = %q, want existing 'big'", got[0].Spec.TargetHost)
	}
}

func TestPlanReservationsRecomputesExistingSpec(t *testing.T) {
	// A host that is already reserved keeps its designation, but its spec is
	// recomputed from the current partition rather than carried over stale. Here
	// the partition grew (hv3 joined), so the kept reservation's SourceHosts must
	// now include hv3 and its stale Resources must be refreshed.
	partition := partitionOf(map[string]int64{"hv1": 64, "hv2": 32, "hv3": 16})
	existing := v1alpha1.Reservation{
		ObjectMeta: metav1.ObjectMeta{Name: "failover-host-hv1"},
		Spec: v1alpha1.ReservationSpec{
			Type:                    v1alpha1.ReservationTypeFailoverHost,
			TargetHost:              "hv1",
			Resources:               res("8Gi", ""), // stale capacity
			FailoverHostReservation: &v1alpha1.FailoverHostReservationSpec{SourceHosts: []string{"hv2"}},
		},
	}
	topo := topology{existing: map[corev1.ObjectReference]v1alpha1.Reservation{
		{Name: existing.Name}: existing,
	}}
	planner := ratioReservationPlanner{ratio: 0.34, minimum: 1} // 3 * 0.34 = 1

	got := planner.PlanReservations(topo, partition, "spec.aggregates=[hana]")
	if len(got) != 1 {
		t.Fatalf("planned %d reservations, want 1", len(got))
	}
	r := got[0]
	if r.Spec.TargetHost != "hv1" {
		t.Fatalf("target host = %q, want kept 'hv1'", r.Spec.TargetHost)
	}
	wantSources := []string{"hv2", "hv3"}
	if r.Spec.FailoverHostReservation == nil ||
		!slices.Equal(r.Spec.FailoverHostReservation.SourceHosts, wantSources) {
		t.Errorf("source hosts = %v, want %v", r.Spec.FailoverHostReservation, wantSources)
	}
	if got := r.Spec.FailoverHostReservation.PartitionDescription; got != "spec.aggregates=[hana]" {
		t.Errorf("partition description = %q, want %q", got, "spec.aggregates=[hana]")
	}
	wantMem := int64(64) * 1024 * 1024 * 1024
	if mem := r.Spec.Resources[hv1.ResourceMemory]; mem.Value() != wantMem {
		t.Errorf("memory = %d, want refreshed %d", mem.Value(), wantMem)
	}
}

func TestPlanReservationsSkipsSingleHostPartition(t *testing.T) {
	// even with minimum >= 1 — reserving it would just waste the host.
	partition := partitionOf(map[string]int64{"solo": 32})
	empty := topology{existing: map[corev1.ObjectReference]v1alpha1.Reservation{}}
	planner := ratioReservationPlanner{ratio: 1, minimum: 1}
	if got := planner.PlanReservations(empty, partition, ""); len(got) != 0 {
		t.Errorf("planned %d reservations for a 1-host partition, want 0", len(got))
	}
}

func TestPlanReservationsLeavesOneSourceHost(t *testing.T) {
	// With minimum larger than the partition, we must still leave at least one
	// host unreserved to run workload; cap desiredCount at len(partition)-1.
	partition := partitionOf(map[string]int64{"hv1": 32, "hv2": 32, "hv3": 32})
	empty := topology{existing: map[corev1.ObjectReference]v1alpha1.Reservation{}}
	planner := ratioReservationPlanner{ratio: 1, minimum: 99}
	if got := planner.PlanReservations(empty, partition, ""); len(got) != len(partition)-1 {
		t.Errorf("planned %d reservations, want %d (one source host kept)", len(got), len(partition)-1)
	}
}
