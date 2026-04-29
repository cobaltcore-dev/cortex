// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package filters

import (
	"log/slog"
	"testing"

	api "github.com/cobaltcore-dev/cortex/api/external/nova"
	"github.com/cobaltcore-dev/cortex/api/v1alpha1"
	hv1 "github.com/cobaltcore-dev/openstack-hypervisor-operator/api/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

// ============================================================================
// Test Helpers
// ============================================================================

func buildTestScheme(t *testing.T) *runtime.Scheme {
	t.Helper()
	scheme := runtime.NewScheme()
	if err := v1alpha1.AddToScheme(scheme); err != nil {
		t.Fatalf("failed to add v1alpha1 scheme: %v", err)
	}
	if err := hv1.SchemeBuilder.AddToScheme(scheme); err != nil {
		t.Fatalf("failed to add hv1 scheme: %v", err)
	}
	return scheme
}

func newHypervisor(name, cpuCap, cpuAlloc, memCap, memAlloc string) *hv1.Hypervisor {
	return &hv1.Hypervisor{
		ObjectMeta: metav1.ObjectMeta{
			Name: name,
		},
		Status: hv1.HypervisorStatus{
			EffectiveCapacity: map[hv1.ResourceName]resource.Quantity{
				hv1.ResourceCPU:    resource.MustParse(cpuCap),
				hv1.ResourceMemory: resource.MustParse(memCap),
			},
			Allocation: map[hv1.ResourceName]resource.Quantity{
				hv1.ResourceCPU:    resource.MustParse(cpuAlloc),
				hv1.ResourceMemory: resource.MustParse(memAlloc),
			},
		},
	}
}

// newHypervisorWithCapacityOnly creates a hypervisor with only Capacity set (no EffectiveCapacity).
func newHypervisorWithCapacityOnly(name, cpuCap, memCap string) *hv1.Hypervisor {
	return &hv1.Hypervisor{
		ObjectMeta: metav1.ObjectMeta{
			Name: name,
		},
		Status: hv1.HypervisorStatus{
			Capacity: map[hv1.ResourceName]resource.Quantity{
				hv1.ResourceCPU:    resource.MustParse(cpuCap),
				hv1.ResourceMemory: resource.MustParse(memCap),
			},
			Allocation: map[hv1.ResourceName]resource.Quantity{
				hv1.ResourceCPU:    resource.MustParse("0"),
				hv1.ResourceMemory: resource.MustParse("0"),
			},
		},
	}
}

// newHypervisorWithBothCapacities creates a hypervisor with both Capacity and EffectiveCapacity set.
// EffectiveCapacity is typically >= Capacity due to overcommit ratio.
func newHypervisorWithBothCapacities(name, cpuCap, cpuEffCap, memCap, memEffCap string) *hv1.Hypervisor {
	return &hv1.Hypervisor{
		ObjectMeta: metav1.ObjectMeta{
			Name: name,
		},
		Status: hv1.HypervisorStatus{
			Capacity: map[hv1.ResourceName]resource.Quantity{
				hv1.ResourceCPU:    resource.MustParse(cpuCap),
				hv1.ResourceMemory: resource.MustParse(memCap),
			},
			EffectiveCapacity: map[hv1.ResourceName]resource.Quantity{
				hv1.ResourceCPU:    resource.MustParse(cpuEffCap),
				hv1.ResourceMemory: resource.MustParse(memEffCap),
			},
			Allocation: map[hv1.ResourceName]resource.Quantity{
				hv1.ResourceCPU:    resource.MustParse("0"),
				hv1.ResourceMemory: resource.MustParse("0"),
			},
		},
	}
}

// newCommittedReservation creates a reservation where TargetHost and Status.Host are the same.
func newCommittedReservation(
	name, host, projectID, flavorName, flavorGroup, cpu, memory string,
	specAllocations map[string]v1alpha1.CommittedResourceAllocation,
	statusAllocations map[string]string,
) *v1alpha1.Reservation {

	return newMigratingReservation(name, host, host, projectID, flavorName, flavorGroup, cpu, memory, specAllocations, statusAllocations)
}

// newMigratingReservation creates a reservation where TargetHost and Status.Host may differ,
// used for in-progress reservation migrations or pending placements.
func newMigratingReservation(
	name, targetHost, observedHost, projectID, flavorName, flavorGroup, cpu, memory string,
	specAllocations map[string]v1alpha1.CommittedResourceAllocation,
	statusAllocations map[string]string,
) *v1alpha1.Reservation {

	res := &v1alpha1.Reservation{
		ObjectMeta: metav1.ObjectMeta{
			Name: name,
		},
		Spec: v1alpha1.ReservationSpec{
			Type:       v1alpha1.ReservationTypeCommittedResource,
			TargetHost: targetHost,
			Resources: map[hv1.ResourceName]resource.Quantity{
				hv1.ResourceCPU:    resource.MustParse(cpu),
				hv1.ResourceMemory: resource.MustParse(memory),
			},
			CommittedResourceReservation: &v1alpha1.CommittedResourceReservationSpec{
				ProjectID:     projectID,
				ResourceName:  flavorName,
				ResourceGroup: flavorGroup,
				Allocations:   specAllocations,
			},
		},
		Status: v1alpha1.ReservationStatus{
			Conditions: []metav1.Condition{
				{
					Type:   v1alpha1.ReservationConditionReady,
					Status: metav1.ConditionTrue,
					Reason: "ReservationActive",
				},
			},
			Host: observedHost,
		},
	}

	if len(statusAllocations) > 0 {
		res.Status.CommittedResourceReservation = &v1alpha1.CommittedResourceReservationStatus{
			Allocations: statusAllocations,
		}
	}

	return res
}

func newFailoverReservation(name, targetHost, cpu, memory string, allocations map[string]string) *v1alpha1.Reservation {
	res := &v1alpha1.Reservation{
		ObjectMeta: metav1.ObjectMeta{
			Name: name,
		},
		Spec: v1alpha1.ReservationSpec{
			Type:       v1alpha1.ReservationTypeFailover,
			TargetHost: targetHost,
			Resources: map[hv1.ResourceName]resource.Quantity{
				hv1.ResourceCPU:    resource.MustParse(cpu),
				hv1.ResourceMemory: resource.MustParse(memory),
			},
			FailoverReservation: &v1alpha1.FailoverReservationSpec{
				ResourceGroup: "m1.large",
			},
		},
		Status: v1alpha1.ReservationStatus{
			Conditions: []metav1.Condition{
				{
					Type:   v1alpha1.ReservationConditionReady,
					Status: metav1.ConditionTrue,
					Reason: "ReservationActive",
				},
			},
			Host: targetHost,
		},
	}
	if allocations != nil {
		res.Status.FailoverReservation = &v1alpha1.FailoverReservationStatus{
			Allocations: allocations,
		}
	}
	return res
}

type crVmAlloc struct {
	uuid string
	cpu  string
	mem  string
}

func crVm(uuid, cpu, mem string) crVmAlloc {
	return crVmAlloc{
		uuid: uuid,
		cpu:  cpu,
		mem:  mem,
	}
}

func crSpecAllocs(vms ...crVmAlloc) map[string]v1alpha1.CommittedResourceAllocation {
	allocs := make(map[string]v1alpha1.CommittedResourceAllocation)
	for _, v := range vms {
		allocs[v.uuid] = v1alpha1.CommittedResourceAllocation{
			CreationTimestamp: metav1.Now(),
			Resources: map[hv1.ResourceName]resource.Quantity{
				hv1.ResourceCPU:    resource.MustParse(v.cpu),
				hv1.ResourceMemory: resource.MustParse(v.mem),
			},
		}
	}
	return allocs
}

// parseMemoryToMB converts a memory string (e.g., "8Gi", "4096Mi") to megabytes.
func parseMemoryToMB(memory string) uint64 {
	q := resource.MustParse(memory)
	bytes := q.Value()
	return uint64(bytes / (1024 * 1024)) //nolint:gosec // test code
}

func newNovaRequest(instanceUUID, projectID, flavorName, flavorGroup string, vcpus int, memory string, evacuation bool, hosts []string) api.ExternalSchedulerRequest { //nolint:unparam // vcpus varies in real usage
	return newNovaRequestWithIntent(instanceUUID, projectID, flavorName, flavorGroup, vcpus, memory, "", evacuation, hosts)
}

// newNovaRequestWithIntent creates a nova request with a specific intent.
// intentHint can be: "evacuate", "reserve_for_committed_resource", "reserve_for_failover", or "" for create.
func newNovaRequestWithIntent(instanceUUID, projectID, flavorName, flavorGroup string, vcpus int, memory, intentHint string, evacuation bool, hosts []string) api.ExternalSchedulerRequest {
	hostList := make([]api.ExternalSchedulerHost, len(hosts))
	for i, h := range hosts {
		hostList[i] = api.ExternalSchedulerHost{ComputeHost: h}
	}

	extraSpecs := map[string]string{
		"capabilities:hypervisor_type": "qemu",
		"hw_version":                   flavorGroup,
	}

	var schedulerHints map[string]any
	if evacuation {
		schedulerHints = map[string]any{
			"_nova_check_type": []any{"evacuate"},
		}
	} else if intentHint != "" {
		schedulerHints = map[string]any{
			"_nova_check_type": intentHint,
		}
	}

	memoryMB := parseMemoryToMB(memory)

	spec := api.NovaSpec{
		ProjectID:      projectID,
		InstanceUUID:   instanceUUID,
		NumInstances:   1,
		SchedulerHints: schedulerHints,
		Flavor: api.NovaObject[api.NovaFlavor]{
			Data: api.NovaFlavor{
				Name:       flavorName,
				VCPUs:      uint64(vcpus), //nolint:gosec // test code
				MemoryMB:   memoryMB,
				ExtraSpecs: extraSpecs,
			},
		},
	}

	weights := make(map[string]float64)
	for _, h := range hosts {
		weights[h] = 1.0
	}

	return api.ExternalSchedulerRequest{
		Spec:    api.NovaObject[api.NovaSpec]{Data: spec},
		Hosts:   hostList,
		Weights: weights,
	}
}

func assertActivations(t *testing.T, activations map[string]float64, expectedHosts, filteredHosts []string) {
	t.Helper()
	for _, host := range expectedHosts {
		if _, ok := activations[host]; !ok {
			t.Errorf("expected host %s to pass, got activations: %v", host, activations)
		}
	}
	for _, host := range filteredHosts {
		if _, ok := activations[host]; ok {
			t.Errorf("expected host %s to be filtered, got activations: %v", host, activations)
		}
	}
}

// ============================================================================
// Tests
// ============================================================================

func TestFilterHasEnoughCapacityOpts_Validate(t *testing.T) {
	tests := []struct {
		name        string
		opts        FilterHasEnoughCapacityOpts
		expectError bool
	}{
		{"valid options with lock reserved true", FilterHasEnoughCapacityOpts{LockReserved: true}, false},
		{"valid options with lock reserved false", FilterHasEnoughCapacityOpts{LockReserved: false}, false},
		{"valid options with default values", FilterHasEnoughCapacityOpts{}, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.opts.Validate()
			if tt.expectError && err == nil {
				t.Error("expected error, got nil")
			}
			if !tt.expectError && err != nil {
				t.Errorf("expected no error, got %v", err)
			}
		})
	}
}

func TestFilterHasEnoughCapacity_ReservationTypes(t *testing.T) {
	scheme := buildTestScheme(t)

	// 4 hypervisors: 3 with capacity, 1 without
	// host1: 8 CPU free, 16Gi free | host2: 4 CPU free, 8Gi free | host3: 16 CPU free, 32Gi free | host4: 0 free
	hypervisors := []*hv1.Hypervisor{
		newHypervisor("host1", "16", "8", "32Gi", "16Gi"),
		newHypervisor("host2", "8", "4", "16Gi", "8Gi"),
		newHypervisor("host3", "32", "16", "64Gi", "32Gi"),
		newHypervisor("host4", "8", "8", "16Gi", "16Gi"), // no capacity
	}

	tests := []struct {
		name          string
		reservations  []*v1alpha1.Reservation
		request       api.ExternalSchedulerRequest
		opts          FilterHasEnoughCapacityOpts
		expectedHosts []string
		filteredHosts []string
	}{
		{
			name:          "No reservations - all hosts with capacity pass",
			reservations:  []*v1alpha1.Reservation{},
			request:       newNovaRequest("instance-123", "project-A", "m1.small", "gp-1", 4, "8Gi", false, []string{"host1", "host2", "host3", "host4"}),
			opts:          FilterHasEnoughCapacityOpts{LockReserved: false},
			expectedHosts: []string{"host1", "host2", "host3"},
			filteredHosts: []string{"host4"},
		},
		{
			name: "CommittedResourceReservation of other project blocks some hosts",
			reservations: []*v1alpha1.Reservation{
				newCommittedReservation("res-1", "host1", "project-A", "m1.large", "gp-1", "8", "16Gi", nil, nil),
				newCommittedReservation("res-2", "host2", "project-A", "m1.large", "gp-1", "4", "8Gi", nil, nil),
			},
			request:       newNovaRequest("instance-123", "project-B", "m1.small", "gp-1", 4, "8Gi", false, []string{"host1", "host2", "host3", "host4"}),
			opts:          FilterHasEnoughCapacityOpts{LockReserved: false},
			expectedHosts: []string{"host3"},
			filteredHosts: []string{"host1", "host2", "host4"},
		},
		{
			name: "CommittedResourceReservation of other project blocks only unused resources of reservation",
			reservations: []*v1alpha1.Reservation{
				newCommittedReservation("res-1 half used", "host1", "project-A", "m1.large", "gp-1", "8", "16Gi", crSpecAllocs(crVm("vm-1", "4", "8Gi")), map[string]string{"vm-1": "host1"}),
				newCommittedReservation("res-2 fully used", "host2", "project-A", "m1.large", "gp-1", "4", "8Gi", crSpecAllocs(crVm("vm-2", "4", "8Gi")), map[string]string{"vm-2": "host2"}),
			},
			request:       newNovaRequest("instance-123", "project-B", "m1.small", "gp-1", 4, "8Gi", false, []string{"host1", "host2", "host3", "host4"}),
			opts:          FilterHasEnoughCapacityOpts{LockReserved: false},
			expectedHosts: []string{"host1", "host2", "host3"},
			filteredHosts: []string{"host4"},
		},
		{
			// host1: 8 CPU free, 16Gi free.
			// Slot=8cpu/16Gi, two confirmed VMs: vm-1=3cpu/6Gi + vm-2=2cpu/4Gi.
			// Correct: confirmed sum=5cpu/10Gi → remaining=3cpu/6Gi → block=3cpu/6Gi → free=5cpu/10Gi.
			// Bug (only one VM counted): block=5cpu/10Gi → free=3cpu/6Gi → 4-cpu request wrongly filtered.
			name: "CommittedResourceReservation blocks only remaining capacity when multiple VMs are confirmed in one slot",
			reservations: []*v1alpha1.Reservation{
				newCommittedReservation("res-multi-vm", "host1", "project-A", "m1.large", "gp-1", "8", "16Gi",
					crSpecAllocs(crVm("vm-1", "3", "6Gi"), crVm("vm-2", "2", "4Gi")),
					map[string]string{"vm-1": "host1", "vm-2": "host1"},
				),
			},
			request:       newNovaRequest("instance-123", "project-B", "m1.small", "gp-1", 4, "8Gi", false, []string{"host1", "host2", "host3", "host4"}),
			opts:          FilterHasEnoughCapacityOpts{LockReserved: false},
			expectedHosts: []string{"host1", "host2", "host3"}, // host1: 5cpu/10Gi free → 4cpu/8Gi request passes
			filteredHosts: []string{"host4"},
		},
		{
			name: "CommittedResourceReservation of other project blocks both source and target host during migration, ignoring used resources",
			reservations: []*v1alpha1.Reservation{
				newCommittedReservation("res-1", "host1", "project-A", "m1.large", "gp-1", "4", "8Gi", nil, nil),
				newMigratingReservation("res-2", "host1", "host2", "project-A", "m1.large", "gp-1", "2", "4Gi", nil, nil),                                                                   // migration reservation from host1 to host2
				newMigratingReservation("res-3", "host2", "host1", "project-A", "m1.large", "gp-1", "2", "4Gi", crSpecAllocs(crVm("vm-1", "2", "4Gi")), map[string]string{"vm-1": "host1"}), // migration reservation from host2 to host1
			},
			request:       newNovaRequest("instance-123", "project-B", "m1.small", "gp-1", 2, "4Gi", false, []string{"host1", "host2", "host3", "host4"}),
			opts:          FilterHasEnoughCapacityOpts{LockReserved: false},
			expectedHosts: []string{"host3"},
			filteredHosts: []string{"host1", "host2", "host4"},
		},
		{
			name: "CommittedResourceReservation unlocks for matching project and flavor group",
			reservations: []*v1alpha1.Reservation{
				// all three reservations 1,2,3 are required to have enough capacity for the request
				newCommittedReservation("res-1-unused", "host1", "project-A", "some flavor", "gp-1", "2", "4Gi", nil, nil),                                                                 // fully unused reservation
				newCommittedReservation("res-2-pending-used", "host1", "project-A", "some flavor", "gp-1", "2", "4Gi", crSpecAllocs(crVm("vm-1", "2", "4Gi")), nil),                        // reservation with a pending allocation
				newCommittedReservation("res-3-used", "host1", "project-A", "some flavor", "gp-1", "2", "4Gi", crSpecAllocs(crVm("vm-2", "1", "1Gi")), map[string]string{"vm-2": "host1"}), // used reservation
				newCommittedReservation("res-4", "host2", "project-A", "some flavor", "gp-2", "4", "8Gi", nil, nil),                                                                        // different flavor group, should still block
			},
			request:       newNovaRequest("instance-123", "project-A", "m1.large", "gp-1", 8, "16Gi", false, []string{"host1", "host2", "host3", "host4"}),
			opts:          FilterHasEnoughCapacityOpts{LockReserved: false},
			expectedHosts: []string{"host1", "host3"},
			filteredHosts: []string{"host4", "host2"},
		},
		{
			name: "CommittedResourceReservation stays locked when LockReserved is true",
			reservations: []*v1alpha1.Reservation{
				newCommittedReservation("res-1", "host1", "project-A", "m1.large", "gp-1", "8", "16Gi", nil, nil),
				newCommittedReservation("res-2", "host3", "project-A", "m1.large", "gp-1", "16", "32Gi", nil, nil),
			},
			request:       newNovaRequest("instance-123", "project-A", "m1.large", "gp-1", 4, "8Gi", false, []string{"host1", "host2", "host3", "host4"}),
			opts:          FilterHasEnoughCapacityOpts{LockReserved: true},
			expectedHosts: []string{"host2"},
			filteredHosts: []string{"host1", "host3", "host4"},
		},
		{
			name: "Empty reservation type defaults to CommittedResourceReservation behavior",
			reservations: []*v1alpha1.Reservation{
				newCommittedReservation("legacy-res", "host1", "project-A", "m1.large", "gp-1", "4", "8Gi", nil, nil),
			},
			request:       newNovaRequest("instance-123", "project-A", "m1.large", "gp-1", 4, "8Gi", false, []string{"host1", "host2"}),
			opts:          FilterHasEnoughCapacityOpts{LockReserved: false},
			expectedHosts: []string{"host1", "host2"},
			filteredHosts: []string{},
		},
		{
			name: "All hosts blocked by reservations - none pass",
			reservations: []*v1alpha1.Reservation{
				newCommittedReservation("res-1", "host1", "project-X", "m1.xlarge", "gp-1", "8", "16Gi", nil, nil),
				newCommittedReservation("res-2", "host2", "project-X", "m1.xlarge", "gp-1", "4", "8Gi", nil, nil),
				newCommittedReservation("res-3", "host3", "project-X", "m1.xlarge", "gp-1", "16", "32Gi", nil, nil),
			},
			request:       newNovaRequest("instance-123", "project-A", "m1.small", "gp-1", 4, "8Gi", false, []string{"host1", "host2", "host3", "host4"}),
			opts:          FilterHasEnoughCapacityOpts{LockReserved: false},
			expectedHosts: []string{},
			filteredHosts: []string{"host1", "host2", "host3", "host4"},
		},
		{
			name: "Pending reservation (only TargetHost set) blocks capacity on desired host",
			reservations: []*v1alpha1.Reservation{
				newMigratingReservation("pending-res", "host1", "", "project-X", "m1.large", "gp-1", "8", "16Gi", nil, nil),
			},
			request:       newNovaRequest("instance-123", "project-A", "m1.small", "gp-1", 4, "8Gi", false, []string{"host1", "host2", "host3", "host4"}),
			opts:          FilterHasEnoughCapacityOpts{LockReserved: false},
			expectedHosts: []string{"host2", "host3"}, // host1 blocked by pending reservation
			filteredHosts: []string{"host1", "host4"},
		},
		{
			name: "Multiple reservations: pending and placed block different hosts",
			reservations: []*v1alpha1.Reservation{
				newMigratingReservation("pending-res", "host1", "", "project-X", "m1.large", "gp-1", "8", "16Gi", nil, nil),
				newMigratingReservation("placed-res", "host2", "host3", "project-X", "m1.large", "gp-1", "4", "8Gi", nil, nil),
			},
			request:       newNovaRequest("instance-123", "project-A", "m1.small", "gp-1", 4, "8Gi", false, []string{"host1", "host2", "host3", "host4"}),
			opts:          FilterHasEnoughCapacityOpts{LockReserved: false},
			expectedHosts: []string{"host3"}, // host1 blocked by pending, host2 blocked by placed, host3 still has capacity
			filteredHosts: []string{"host1", "host2", "host4"},
		},
		{
			name: "Reservation with no host is skipped",
			reservations: []*v1alpha1.Reservation{
				newCommittedReservation("no-host-res", "", "project-X", "m1.large", "gp-1", "8", "16Gi", nil, nil),
			},
			request:       newNovaRequest("instance-123", "project-A", "m1.small", "gp-1", 4, "8Gi", false, []string{"host1", "host2", "host3", "host4"}),
			opts:          FilterHasEnoughCapacityOpts{LockReserved: false},
			expectedHosts: []string{"host1", "host2", "host3"},
			filteredHosts: []string{"host4"},
		},
		{
			// host1: 8 CPU free, 16Gi free (shared hypervisors).
			// Reservation slot=4cpu/8Gi, but confirmed VM consumed 6cpu/10Gi (exceeds slot after resize).
			// Unclamped: block = -2cpu/-2Gi → free becomes {10cpu,18Gi}; passes 9-cpu request (wrong).
			// Clamped:   block = 0          → free stays {8cpu,16Gi}; filtered for 9-cpu request.
			name: "Confirmed VMs exceeding reservation size: block clamped to 0",
			reservations: []*v1alpha1.Reservation{
				newCommittedReservation("res-oversized-vm", "host1", "project-X", "m1.large", "gp-1", "4", "8Gi",
					crSpecAllocs(crVm("vm-1", "6", "10Gi")),
					map[string]string{"vm-1": "host1"},
				),
			},
			request: newNovaRequest("instance-123", "project-A", "m1.small", "gp-1", 9, "17Gi", false, []string{"host1", "host2", "host3", "host4"}),
			opts:    FilterHasEnoughCapacityOpts{LockReserved: false},
			// host1: 8 free CPU < 9, 16Gi < 17Gi → filtered; host2: 4 < 9 → filtered; host4: no capacity → filtered
			expectedHosts: []string{"host3"},
			filteredHosts: []string{"host1", "host2", "host4"},
		},
		{
			// host1: 8 CPU free, 16Gi free (shared hypervisors).
			// Reservation slot=8cpu/16Gi, confirmed vm-1=4cpu/8Gi (remaining={4,8Gi}).
			// Spec-only vm-2=6cpu/12Gi EXCEEDS remaining → block must be {6,12Gi}, not {4,8Gi}.
			// Without fix: block={4,8Gi} → free={4cpu,8Gi}; 3-cpu/5Gi request passes (wrong).
			// With fix:    block={6,12Gi} → free={2cpu,4Gi}; filtered for 3-cpu/5Gi request.
			name: "Spec-only VM larger than remaining slot: block covers spec-only VM",
			reservations: []*v1alpha1.Reservation{
				newCommittedReservation("res-spec-only-oversize", "host1", "project-X", "m1.large", "gp-1", "8", "16Gi",
					crSpecAllocs(crVm("vm-1", "4", "8Gi"), crVm("vm-2", "6", "12Gi")),
					map[string]string{"vm-1": "host1"}, // vm-2 is spec-only
				),
			},
			request: newNovaRequest("instance-123", "project-A", "m1.small", "gp-1", 3, "5Gi", false, []string{"host1", "host2", "host3", "host4"}),
			opts:    FilterHasEnoughCapacityOpts{LockReserved: false},
			// host1: 2 free CPU < 3 requested, 4Gi < 5Gi → filtered; host4: no capacity → filtered
			expectedHosts: []string{"host2", "host3"},
			filteredHosts: []string{"host1", "host4"},
		},
		{
			name: "FailoverReservation blocks hosts for non-evacuation request even when instance is in Allocations",
			reservations: []*v1alpha1.Reservation{
				newFailoverReservation("failover-1", "host1", "8", "16Gi", map[string]string{"instance-123": "host5"}),
				newFailoverReservation("failover-2", "host2", "4", "8Gi", map[string]string{"instance-123": "host6"}),
			},
			request:       newNovaRequest("instance-123", "project-A", "m1.large", "gp-1", 4, "8Gi", false, []string{"host1", "host2", "host3", "host4"}),
			opts:          FilterHasEnoughCapacityOpts{LockReserved: false},
			expectedHosts: []string{"host3"},
			filteredHosts: []string{"host1", "host2", "host4"},
		},
		{
			name: "FailoverReservation unlocks hosts during evacuation when instance is in Allocations",
			reservations: []*v1alpha1.Reservation{
				newFailoverReservation("failover-1", "host1", "4", "8Gi", map[string]string{"instance-123": "host5"}),
				newFailoverReservation("failover-2", "host2", "4", "8Gi", map[string]string{"instance-123": "host6"}),
			},
			request:       newNovaRequest("instance-123", "project-A", "m1.large", "gp-1", 4, "8Gi", true, []string{"host1", "host2", "host3", "host4"}),
			opts:          FilterHasEnoughCapacityOpts{LockReserved: false},
			expectedHosts: []string{"host1", "host2", "host3"},
			filteredHosts: []string{"host4"},
		},
		{
			name: "FailoverReservation blocks hosts during evacuation when instance not in Allocations",
			reservations: []*v1alpha1.Reservation{
				newFailoverReservation("failover-1", "host1", "8", "16Gi", map[string]string{"other-instance": "host5"}),
				newFailoverReservation("failover-2", "host2", "4", "8Gi", map[string]string{"another-instance": "host6"}),
			},
			request:       newNovaRequest("instance-123", "project-A", "m1.large", "gp-1", 4, "8Gi", true, []string{"host1", "host2", "host3", "host4"}),
			opts:          FilterHasEnoughCapacityOpts{LockReserved: false},
			expectedHosts: []string{"host3"},
			filteredHosts: []string{"host1", "host2", "host4"},
		},
		{
			name: "FailoverReservation with empty Allocations blocks reserved host",
			reservations: []*v1alpha1.Reservation{
				newFailoverReservation("failover-1", "host1", "8", "16Gi", map[string]string{}),
			},
			request:       newNovaRequest("instance-123", "project-A", "m1.large", "gp-1", 4, "8Gi", true, []string{"host1", "host2", "host3"}),
			opts:          FilterHasEnoughCapacityOpts{LockReserved: false},
			expectedHosts: []string{"host2", "host3"},
			filteredHosts: []string{"host1"},
		},
		{
			name: "FailoverReservation with multiple instances in Allocations unlocks for matching instance during evacuation",
			reservations: []*v1alpha1.Reservation{
				newFailoverReservation("failover-1", "host1", "4", "8Gi", map[string]string{"instance-111": "host5", "instance-222": "host6", "instance-333": "host7"}),
			},
			request:       newNovaRequest("instance-222", "project-A", "m1.large", "gp-1", 4, "8Gi", true, []string{"host1", "host2", "host3", "host4"}),
			opts:          FilterHasEnoughCapacityOpts{LockReserved: false},
			expectedHosts: []string{"host1", "host2", "host3"},
			filteredHosts: []string{"host4"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			objects := make([]client.Object, 0, len(hypervisors)+len(tt.reservations))
			for _, h := range hypervisors {
				objects = append(objects, h.DeepCopy())
			}
			for _, r := range tt.reservations {
				objects = append(objects, r.DeepCopy())
			}

			step := &FilterHasEnoughCapacity{}
			step.Client = fake.NewClientBuilder().WithScheme(scheme).WithObjects(objects...).Build()
			step.Options = tt.opts

			result, err := step.Run(slog.Default(), tt.request)
			if err != nil {
				t.Fatalf("expected no error, got %v", err)
			}
			assertActivations(t, result.Activations, tt.expectedHosts, tt.filteredHosts)
		})
	}
}

func TestFilterHasEnoughCapacity_IgnoredReservationTypes(t *testing.T) {
	scheme := buildTestScheme(t)

	// Two-host scenario: CR on host1 (4cpu/8Gi, project-X), Failover on host2 (4cpu/8Gi).
	// Each host: 8 CPU free after base allocation → after reservation: 4 CPU free each.
	twoHostHVs := []*hv1.Hypervisor{
		newHypervisor("host1", "16", "8", "32Gi", "16Gi"),
		newHypervisor("host2", "16", "8", "32Gi", "16Gi"),
	}
	twoHostRes := []*v1alpha1.Reservation{
		newCommittedReservation("cr-res", "host1", "project-X", "m1.large", "gp-1", "4", "8Gi", nil, nil),
		newFailoverReservation("failover-res", "host2", "4", "8Gi", map[string]string{"other-vm": "host3"}),
	}

	// Single-host scenario: both CR (4cpu/8Gi) and Failover (2cpu/4Gi) on host1.
	// host1: 12 CPU free → after both reservations: 6 CPU free.
	singleHostHVs := []*hv1.Hypervisor{
		newHypervisor("host1", "12", "0", "24Gi", "0"),
	}
	singleHostRes := []*v1alpha1.Reservation{
		newCommittedReservation("cr-res", "host1", "project-X", "m1.large", "gp-1", "4", "8Gi", nil, nil),
		newFailoverReservation("failover-res", "host1", "2", "4Gi", map[string]string{"other-vm": "host3"}),
	}

	tests := []struct {
		name                    string
		hypervisors             []*hv1.Hypervisor
		reservations            []*v1alpha1.Reservation
		request                 api.ExternalSchedulerRequest
		ignoredReservationTypes []v1alpha1.ReservationType
		expectedHosts           []string
		filteredHosts           []string
	}{
		// Two-host scenario tests (CR on host1, Failover on host2)
		// host1: 8 CPU free, host2: 8 CPU free, CR blocks 4 on host1, Failover blocks 4 on host2
		{
			name:                    "Two hosts: No ignore - both hosts blocked by reservations",
			hypervisors:             twoHostHVs,
			reservations:            twoHostRes,
			request:                 newNovaRequest("instance-123", "project-A", "m1.large", "gp-1", 8, "16Gi", false, []string{"host1", "host2"}),
			ignoredReservationTypes: nil,
			expectedHosts:           []string{},
			filteredHosts:           []string{"host1", "host2"},
		},
		{
			name:                    "Two hosts: Ignore CR only - host1 passes, host2 blocked by failover",
			hypervisors:             twoHostHVs,
			reservations:            twoHostRes,
			request:                 newNovaRequest("instance-123", "project-A", "m1.large", "gp-1", 8, "16Gi", false, []string{"host1", "host2"}),
			ignoredReservationTypes: []v1alpha1.ReservationType{v1alpha1.ReservationTypeCommittedResource},
			expectedHosts:           []string{"host1"},
			filteredHosts:           []string{"host2"},
		},
		{
			name:                    "Two hosts: Ignore Failover only - host2 passes, host1 blocked by CR",
			hypervisors:             twoHostHVs,
			reservations:            twoHostRes,
			request:                 newNovaRequest("instance-123", "project-A", "m1.large", "gp-1", 8, "16Gi", false, []string{"host1", "host2"}),
			ignoredReservationTypes: []v1alpha1.ReservationType{v1alpha1.ReservationTypeFailover},
			expectedHosts:           []string{"host2"},
			filteredHosts:           []string{"host1"},
		},
		{
			name:                    "Two hosts: Ignore both - both hosts pass",
			hypervisors:             twoHostHVs,
			reservations:            twoHostRes,
			request:                 newNovaRequest("instance-123", "project-A", "m1.large", "gp-1", 8, "16Gi", false, []string{"host1", "host2"}),
			ignoredReservationTypes: []v1alpha1.ReservationType{v1alpha1.ReservationTypeCommittedResource, v1alpha1.ReservationTypeFailover},
			expectedHosts:           []string{"host1", "host2"},
			filteredHosts:           []string{},
		},

		// Single-host scenario tests (both CR and Failover on host1)
		// host1: 12 CPU free, CR blocks 4, Failover blocks 2 → 6 free when both active
		// Large VM (12 CPU) - only fits if BOTH reservations are ignored
		{
			name:                    "Single host, Large VM (12 CPU): No ignore - blocked (6 free < 12 needed)",
			hypervisors:             singleHostHVs,
			reservations:            singleHostRes,
			request:                 newNovaRequest("instance-123", "project-A", "m1.large", "gp-1", 12, "24Gi", false, []string{"host1"}),
			ignoredReservationTypes: nil,
			expectedHosts:           []string{},
			filteredHosts:           []string{"host1"},
		},
		{
			name:                    "Single host, Large VM (12 CPU): Ignore CR - blocked (10 free < 12 needed)",
			hypervisors:             singleHostHVs,
			reservations:            singleHostRes,
			request:                 newNovaRequest("instance-123", "project-A", "m1.large", "gp-1", 12, "24Gi", false, []string{"host1"}),
			ignoredReservationTypes: []v1alpha1.ReservationType{v1alpha1.ReservationTypeCommittedResource},
			expectedHosts:           []string{},
			filteredHosts:           []string{"host1"},
		},
		{
			name:                    "Single host, Large VM (12 CPU): Ignore Failover - blocked (8 free < 12 needed)",
			hypervisors:             singleHostHVs,
			reservations:            singleHostRes,
			request:                 newNovaRequest("instance-123", "project-A", "m1.large", "gp-1", 12, "24Gi", false, []string{"host1"}),
			ignoredReservationTypes: []v1alpha1.ReservationType{v1alpha1.ReservationTypeFailover},
			expectedHosts:           []string{},
			filteredHosts:           []string{"host1"},
		},
		{
			name:                    "Single host, Large VM (12 CPU): Ignore both - passes (12 free = 12 needed)",
			hypervisors:             singleHostHVs,
			reservations:            singleHostRes,
			request:                 newNovaRequest("instance-123", "project-A", "m1.large", "gp-1", 12, "24Gi", false, []string{"host1"}),
			ignoredReservationTypes: []v1alpha1.ReservationType{v1alpha1.ReservationTypeCommittedResource, v1alpha1.ReservationTypeFailover},
			expectedHosts:           []string{"host1"},
			filteredHosts:           []string{},
		},

		// Failover-size VM (10 CPU) - fits if CR is ignored (10 free = 10 needed)
		{
			name:                    "Single host, Failover-size VM (10 CPU): No ignore - blocked (6 free < 10 needed)",
			hypervisors:             singleHostHVs,
			reservations:            singleHostRes,
			request:                 newNovaRequest("instance-123", "project-A", "m1.large", "gp-1", 10, "20Gi", false, []string{"host1"}),
			ignoredReservationTypes: nil,
			expectedHosts:           []string{},
			filteredHosts:           []string{"host1"},
		},
		{
			name:                    "Single host, Failover-size VM (10 CPU): Ignore CR - passes (10 free = 10 needed)",
			hypervisors:             singleHostHVs,
			reservations:            singleHostRes,
			request:                 newNovaRequest("instance-123", "project-A", "m1.large", "gp-1", 10, "20Gi", false, []string{"host1"}),
			ignoredReservationTypes: []v1alpha1.ReservationType{v1alpha1.ReservationTypeCommittedResource},
			expectedHosts:           []string{"host1"},
			filteredHosts:           []string{},
		},
		{
			name:                    "Single host, Failover-size VM (10 CPU): Ignore Failover - blocked (8 free < 10 needed)",
			hypervisors:             singleHostHVs,
			reservations:            singleHostRes,
			request:                 newNovaRequest("instance-123", "project-A", "m1.large", "gp-1", 10, "20Gi", false, []string{"host1"}),
			ignoredReservationTypes: []v1alpha1.ReservationType{v1alpha1.ReservationTypeFailover},
			expectedHosts:           []string{},
			filteredHosts:           []string{"host1"},
		},
		{
			name:                    "Single host, Failover-size VM (10 CPU): Ignore both - passes (12 free > 10 needed)",
			hypervisors:             singleHostHVs,
			reservations:            singleHostRes,
			request:                 newNovaRequest("instance-123", "project-A", "m1.large", "gp-1", 10, "20Gi", false, []string{"host1"}),
			ignoredReservationTypes: []v1alpha1.ReservationType{v1alpha1.ReservationTypeCommittedResource, v1alpha1.ReservationTypeFailover},
			expectedHosts:           []string{"host1"},
			filteredHosts:           []string{},
		},

		// CR-size VM (8 CPU) - fits if Failover is ignored (8 free = 8 needed)
		{
			name:                    "Single host, CR-size VM (8 CPU): No ignore - blocked (6 free < 8 needed)",
			hypervisors:             singleHostHVs,
			reservations:            singleHostRes,
			request:                 newNovaRequest("instance-123", "project-A", "m1.large", "gp-1", 8, "16Gi", false, []string{"host1"}),
			ignoredReservationTypes: nil,
			expectedHosts:           []string{},
			filteredHosts:           []string{"host1"},
		},
		{
			name:                    "Single host, CR-size VM (8 CPU): Ignore CR - passes (10 free > 8 needed)",
			hypervisors:             singleHostHVs,
			reservations:            singleHostRes,
			request:                 newNovaRequest("instance-123", "project-A", "m1.large", "gp-1", 8, "16Gi", false, []string{"host1"}),
			ignoredReservationTypes: []v1alpha1.ReservationType{v1alpha1.ReservationTypeCommittedResource},
			expectedHosts:           []string{"host1"},
			filteredHosts:           []string{},
		},
		{
			name:                    "Single host, CR-size VM (8 CPU): Ignore Failover - passes (8 free = 8 needed)",
			hypervisors:             singleHostHVs,
			reservations:            singleHostRes,
			request:                 newNovaRequest("instance-123", "project-A", "m1.large", "gp-1", 8, "16Gi", false, []string{"host1"}),
			ignoredReservationTypes: []v1alpha1.ReservationType{v1alpha1.ReservationTypeFailover},
			expectedHosts:           []string{"host1"},
			filteredHosts:           []string{},
		},
		{
			name:                    "Single host, CR-size VM (8 CPU): Ignore both - passes (12 free > 8 needed)",
			hypervisors:             singleHostHVs,
			reservations:            singleHostRes,
			request:                 newNovaRequest("instance-123", "project-A", "m1.large", "gp-1", 8, "16Gi", false, []string{"host1"}),
			ignoredReservationTypes: []v1alpha1.ReservationType{v1alpha1.ReservationTypeCommittedResource, v1alpha1.ReservationTypeFailover},
			expectedHosts:           []string{"host1"},
			filteredHosts:           []string{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			objects := make([]client.Object, 0, len(tt.hypervisors)+len(tt.reservations))
			for _, h := range tt.hypervisors {
				objects = append(objects, h.DeepCopy())
			}
			for _, r := range tt.reservations {
				objects = append(objects, r.DeepCopy())
			}

			step := &FilterHasEnoughCapacity{}
			step.Client = fake.NewClientBuilder().WithScheme(scheme).WithObjects(objects...).Build()
			step.Options = FilterHasEnoughCapacityOpts{
				LockReserved:            true,
				IgnoredReservationTypes: tt.ignoredReservationTypes,
			}

			result, err := step.Run(slog.Default(), tt.request)
			if err != nil {
				t.Fatalf("expected no error, got %v", err)
			}
			assertActivations(t, result.Activations, tt.expectedHosts, tt.filteredHosts)
		})
	}
}

func TestFilterHasEnoughCapacity_ReserveForCommittedResourceIntent(t *testing.T) {
	scheme := buildTestScheme(t)

	// Test that when scheduling a CR reservation (with reserve_for_committed_resource intent),
	// other CR reservations from the same project+flavor group are NOT unlocked.
	// This prevents overbooking when scheduling multiple CR reservations.
	tests := []struct {
		name          string
		hypervisors   []*hv1.Hypervisor
		reservations  []*v1alpha1.Reservation
		request       api.ExternalSchedulerRequest
		opts          FilterHasEnoughCapacityOpts
		expectedHosts []string
		filteredHosts []string
	}{
		{
			name: "CR reservation scheduling: same project+flavor reservations stay locked (prevents overbooking)",
			hypervisors: []*hv1.Hypervisor{
				newHypervisor("host1", "16", "8", "32Gi", "16Gi"), // 8 CPU free
				newHypervisor("host2", "16", "8", "32Gi", "16Gi"), // 8 CPU free
			},
			reservations: []*v1alpha1.Reservation{
				// Existing CR reservation on host1 for same project+flavor group
				newCommittedReservation("existing-cr", "host1", "project-A", "m1.large", "gp-1", "8", "16Gi", nil, nil),
			},
			// Request with reserve_for_committed_resource intent (scheduling a new CR reservation)
			request:       newNovaRequestWithIntent("new-reservation-uuid", "project-A", "m1.large", "gp-1", 4, "8Gi", "reserve_for_committed_resource", false, []string{"host1", "host2"}),
			opts:          FilterHasEnoughCapacityOpts{LockReserved: false}, // Note: LockReserved is false, but intent overrides
			expectedHosts: []string{"host2"},                                // host1 blocked because existing-cr stays locked
			filteredHosts: []string{"host1"},
		},
		{
			name: "Normal VM scheduling: same project+flavor reservations ARE unlocked",
			hypervisors: []*hv1.Hypervisor{
				newHypervisor("host1", "16", "8", "32Gi", "16Gi"), // 8 CPU free
				newHypervisor("host2", "16", "8", "32Gi", "16Gi"), // 8 CPU free
			},
			reservations: []*v1alpha1.Reservation{
				// Existing CR reservation on host1 for same project+flavor group
				newCommittedReservation("existing-cr", "host1", "project-A", "m1.large", "gp-1", "8", "16Gi", nil, nil),
			},
			// Normal VM create request (no special intent) - CR reservation should be unlocked
			request:       newNovaRequest("vm-instance-123", "project-A", "m1.large", "gp-1", 4, "8Gi", false, []string{"host1", "host2"}),
			opts:          FilterHasEnoughCapacityOpts{LockReserved: false},
			expectedHosts: []string{"host1", "host2"}, // host1 passes because existing-cr is unlocked for matching project+flavor
			filteredHosts: []string{},
		},
		{
			name: "CR reservation scheduling: different project reservations stay locked (as expected)",
			hypervisors: []*hv1.Hypervisor{
				newHypervisor("host1", "16", "8", "32Gi", "16Gi"), // 8 CPU free
				newHypervisor("host2", "16", "8", "32Gi", "16Gi"), // 8 CPU free
			},
			reservations: []*v1alpha1.Reservation{
				// Existing CR reservation on host1 for different project
				newCommittedReservation("other-project-cr", "host1", "project-B", "m1.large", "gp-1", "8", "16Gi", nil, nil),
			},
			// Request with reserve_for_committed_resource intent
			request:       newNovaRequestWithIntent("new-reservation-uuid", "project-A", "m1.large", "gp-1", 4, "8Gi", "reserve_for_committed_resource", false, []string{"host1", "host2"}),
			opts:          FilterHasEnoughCapacityOpts{LockReserved: false},
			expectedHosts: []string{"host2"},
			filteredHosts: []string{"host1"}, // host1 blocked by other project's reservation (would be blocked anyway)
		},
		{
			name: "CR reservation scheduling: multiple reservations - none unlocked",
			hypervisors: []*hv1.Hypervisor{
				newHypervisor("host1", "32", "0", "64Gi", "0"), // 32 CPU free
			},
			reservations: []*v1alpha1.Reservation{
				// Three existing CR reservations on host1 for same project+flavor group
				newCommittedReservation("cr-1", "host1", "project-A", "m1.large", "gp-1", "8", "16Gi", nil, nil),
				newCommittedReservation("cr-2", "host1", "project-A", "m1.large", "gp-1", "8", "16Gi", nil, nil),
				newCommittedReservation("cr-3", "host1", "project-A", "m1.large", "gp-1", "8", "16Gi", nil, nil),
			},
			// Request with reserve_for_committed_resource intent, needs 10 CPU
			// After blocking all 3 reservations (24 CPU), only 8 CPU free -> should fail
			request:       newNovaRequestWithIntent("new-reservation-uuid", "project-A", "m1.large", "gp-1", 10, "20Gi", "reserve_for_committed_resource", false, []string{"host1"}),
			opts:          FilterHasEnoughCapacityOpts{LockReserved: false},
			expectedHosts: []string{},
			filteredHosts: []string{"host1"}, // All reservations stay locked, not enough capacity
		},
		{
			name: "Normal VM scheduling: multiple reservations - all unlocked for same project+flavor",
			hypervisors: []*hv1.Hypervisor{
				newHypervisor("host1", "32", "0", "64Gi", "0"), // 32 CPU free
			},
			reservations: []*v1alpha1.Reservation{
				// Three existing CR reservations on host1 for same project+flavor group
				newCommittedReservation("cr-1", "host1", "project-A", "m1.large", "gp-1", "8", "16Gi", nil, nil),
				newCommittedReservation("cr-2", "host1", "project-A", "m1.large", "gp-1", "8", "16Gi", nil, nil),
				newCommittedReservation("cr-3", "host1", "project-A", "m1.large", "gp-1", "8", "16Gi", nil, nil),
			},
			// Normal VM create request, needs 10 CPU
			// All 3 reservations unlocked for matching project+flavor -> 32 CPU free -> should pass
			request:       newNovaRequest("vm-instance-123", "project-A", "m1.large", "gp-1", 10, "20Gi", false, []string{"host1"}),
			opts:          FilterHasEnoughCapacityOpts{LockReserved: false},
			expectedHosts: []string{"host1"}, // All reservations unlocked, enough capacity
			filteredHosts: []string{},
		},
		{
			name: "CR reservation scheduling: IgnoredReservationTypes config DOES bypass intent protection (safety override)",
			hypervisors: []*hv1.Hypervisor{
				newHypervisor("host1", "16", "8", "32Gi", "16Gi"), // 8 CPU free
			},
			reservations: []*v1alpha1.Reservation{
				// Existing CR reservation on host1 blocks all 8 free CPU
				newCommittedReservation("existing-cr", "host1", "project-A", "m1.large", "gp-1", "8", "16Gi", nil, nil),
			},
			// Request with reserve_for_committed_resource intent
			// IgnoredReservationTypes is a safety flag that overrides everything, including intent
			request: newNovaRequestWithIntent("new-reservation-uuid", "project-A", "m1.large", "gp-1", 4, "8Gi", "reserve_for_committed_resource", false, []string{"host1"}),
			opts: FilterHasEnoughCapacityOpts{
				LockReserved: false,
				// IgnoredReservationTypes is a safety override - ignores CR even for CR scheduling
				IgnoredReservationTypes: []v1alpha1.ReservationType{v1alpha1.ReservationTypeCommittedResource},
			},
			expectedHosts: []string{"host1"}, // CR reservation is ignored via IgnoredReservationTypes (safety override)
			filteredHosts: []string{},
		},
		{
			name: "Normal VM scheduling: IgnoredReservationTypes config DOES work for normal VMs",
			hypervisors: []*hv1.Hypervisor{
				newHypervisor("host1", "16", "8", "32Gi", "16Gi"), // 8 CPU free
			},
			reservations: []*v1alpha1.Reservation{
				// Existing CR reservation on host1 blocks all 8 free CPU
				newCommittedReservation("existing-cr", "host1", "project-B", "m1.large", "gp-1", "8", "16Gi", nil, nil),
			},
			// Normal VM create request (different project, so unlocking via project match won't work)
			// But IgnoredReservationTypes should make it work
			request: newNovaRequest("vm-instance-123", "project-A", "m1.large", "gp-1", 4, "8Gi", false, []string{"host1"}),
			opts: FilterHasEnoughCapacityOpts{
				LockReserved:            false,
				IgnoredReservationTypes: []v1alpha1.ReservationType{v1alpha1.ReservationTypeCommittedResource},
			},
			expectedHosts: []string{"host1"}, // CR reservation ignored via IgnoredReservationTypes
			filteredHosts: []string{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			objects := make([]client.Object, 0, len(tt.hypervisors)+len(tt.reservations))
			for _, h := range tt.hypervisors {
				objects = append(objects, h.DeepCopy())
			}
			for _, r := range tt.reservations {
				objects = append(objects, r.DeepCopy())
			}

			step := &FilterHasEnoughCapacity{}
			step.Client = fake.NewClientBuilder().WithScheme(scheme).WithObjects(objects...).Build()
			step.Options = tt.opts

			result, err := step.Run(slog.Default(), tt.request)
			if err != nil {
				t.Fatalf("expected no error, got %v", err)
			}
			assertActivations(t, result.Activations, tt.expectedHosts, tt.filteredHosts)
		})
	}
}

// TestFilterHasEnoughCapacity_PlannedCRDoesNotBlock verifies that a CommittedResource CRD
// in "planned" state has no child Reservation CRDs and therefore blocks no capacity.
// This is correct by design: the filter reads only Reservation CRDs, so planned CRDs
// have no effect regardless of the committed amount.
func TestFilterHasEnoughCapacity_PlannedCRDoesNotBlock(t *testing.T) {
	scheme := buildTestScheme(t)

	// A planned CommittedResource: StartTime not yet reached, no Reservation CRDs created.
	plannedCR := &v1alpha1.CommittedResource{
		ObjectMeta: metav1.ObjectMeta{Name: "cr-planned-uuid-1"},
		Spec: v1alpha1.CommittedResourceSpec{
			CommitmentUUID:   "uuid-1",
			ProjectID:        "project-A",
			DomainID:         "domain-A",
			FlavorGroupName:  "gp-1",
			ResourceType:     v1alpha1.CommittedResourceTypeMemory,
			Amount:           resource.MustParse("16Gi"),
			AvailabilityZone: "az-1",
			State:            v1alpha1.CommitmentStatusPlanned,
		},
		Status: v1alpha1.CommittedResourceStatus{
			Conditions: []metav1.Condition{
				{
					Type:               v1alpha1.CommittedResourceConditionReady,
					Status:             metav1.ConditionFalse,
					Reason:             v1alpha1.CommittedResourceReasonPlanned,
					LastTransitionTime: metav1.Now(),
				},
			},
		},
	}

	hv := newHypervisor("host1", "16", "8", "32Gi", "16Gi") // 8 CPU free, 16Gi free
	objects := []client.Object{hv, plannedCR}
	// No Reservation CRDs — planned CommittedResources have none.

	step := &FilterHasEnoughCapacity{}
	step.Client = fake.NewClientBuilder().WithScheme(scheme).WithObjects(objects...).Build()
	step.Options = FilterHasEnoughCapacityOpts{LockReserved: false}

	request := newNovaRequest("instance-123", "project-A", "m1.large", "gp-1", 4, "8Gi", false, []string{"host1"})
	result, err := step.Run(slog.Default(), request)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if _, ok := result.Activations["host1"]; !ok {
		t.Error("expected host1 to pass: planned CommittedResource has no child Reservations and must not block capacity")
	}
}

func TestFilterHasEnoughCapacity_NilEffectiveCapacityFallback(t *testing.T) {
	scheme := buildTestScheme(t)

	tests := []struct {
		name          string
		hypervisors   []*hv1.Hypervisor
		request       api.ExternalSchedulerRequest
		expectedHosts []string
		filteredHosts []string
	}{
		{
			name: "Hypervisor with nil EffectiveCapacity uses Capacity fallback",
			hypervisors: []*hv1.Hypervisor{
				newHypervisor("host1", "16", "8", "32Gi", "16Gi"),    // has EffectiveCapacity: 8 CPU free, 16Gi free
				newHypervisorWithCapacityOnly("host2", "8", "16Gi"),  // nil EffectiveCapacity, uses Capacity: 8 CPU free, 16Gi free
				newHypervisorWithCapacityOnly("host3", "2", "4Gi"),   // nil EffectiveCapacity, uses Capacity: 2 CPU free (not enough)
				newHypervisorWithCapacityOnly("host4", "16", "32Gi"), // nil EffectiveCapacity, uses Capacity: 16 CPU free, 32Gi free
			},
			request:       newNovaRequest("instance-123", "project-A", "m1.small", "gp-1", 4, "8Gi", false, []string{"host1", "host2", "host3", "host4"}),
			expectedHosts: []string{"host1", "host2", "host4"},
			filteredHosts: []string{"host3"},
		},
		{
			name: "All hypervisors with nil EffectiveCapacity use Capacity fallback",
			hypervisors: []*hv1.Hypervisor{
				newHypervisorWithCapacityOnly("host1", "8", "16Gi"),
				newHypervisorWithCapacityOnly("host2", "4", "8Gi"),
			},
			request:       newNovaRequest("instance-123", "project-A", "m1.small", "gp-1", 4, "8Gi", false, []string{"host1", "host2"}),
			expectedHosts: []string{"host1", "host2"},
			filteredHosts: []string{},
		},
		{
			name: "EffectiveCapacity used when both are set (overcommit scenario)",
			hypervisors: []*hv1.Hypervisor{
				// host1: Capacity=8 CPU, EffectiveCapacity=16 CPU (2x overcommit)
				// With Capacity only: 8 free -> passes
				// With EffectiveCapacity: 16 free -> passes (more capacity available)
				newHypervisorWithBothCapacities("host1", "8", "16", "16Gi", "32Gi"),
				// host2: Capacity=4 CPU, EffectiveCapacity=8 CPU (2x overcommit)
				// With Capacity only: 4 free -> would be filtered (need 5)
				// With EffectiveCapacity: 8 free -> passes
				newHypervisorWithBothCapacities("host2", "4", "8", "8Gi", "16Gi"),
				// host3: Capacity=4 CPU, EffectiveCapacity=4 CPU (no overcommit)
				// Both: 4 free -> filtered (need 5)
				newHypervisorWithBothCapacities("host3", "4", "4", "8Gi", "8Gi"),
			},
			request:       newNovaRequest("instance-123", "project-A", "m1.small", "gp-1", 5, "8Gi", false, []string{"host1", "host2", "host3"}),
			expectedHosts: []string{"host1", "host2"},
			filteredHosts: []string{"host3"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			objects := make([]client.Object, 0, len(tt.hypervisors))
			for _, h := range tt.hypervisors {
				objects = append(objects, h.DeepCopy())
			}

			step := &FilterHasEnoughCapacity{}
			step.Client = fake.NewClientBuilder().WithScheme(scheme).WithObjects(objects...).Build()
			step.Options = FilterHasEnoughCapacityOpts{LockReserved: false}

			result, err := step.Run(slog.Default(), tt.request)
			if err != nil {
				t.Fatalf("expected no error, got %v", err)
			}
			assertActivations(t, result.Activations, tt.expectedHosts, tt.filteredHosts)
		})
	}
}

// TestFilterHasEnoughCapacity_VMInterReservationMigration covers all realistic phases of a VM
// migrating from res-a (on hv-a) to res-b (on hv-b).
//
// Six binary state variables per phase:
//   - VM in hv-a allocation (affects hv-a free capacity directly)
//   - VM in hv-b allocation (affects hv-b free capacity directly)
//   - VM in res-a Spec.Allocations
//   - VM in res-a Status.Allocations
//   - VM in res-b Spec.Allocations
//   - VM in res-b Status.Allocations
//
// Capacity accounting per host:
//
//	free  = HV.EffectiveCapacity - HV.Allocation
//	block = max(slot - confirmed, specOnly)   [clamped ≥ 0; else full slot when spec allocs empty]
//	net   = free - block
//
// All phases use: VM=4cpu/8Gi, slot=8cpu/16Gi, HV total=12cpu/24Gi, request=3cpu/6Gi (project-C).
func TestFilterHasEnoughCapacity_VMInterReservationMigration(t *testing.T) {
	scheme := buildTestScheme(t)

	const (
		owner       = "project-A" // project owning the reservations and the migrating VM
		thirdParty  = "project-C" // project making the placement request
		flavorGroup = "gp-1"
		slotCPU     = "8"
		slotMem     = "16Gi"
		hvCapCPU    = "12"
		hvCapMem    = "24Gi"
		vmCPU       = "4"
		vmMem       = "8Gi"
	)

	tests := []struct {
		name          string
		hvA           *hv1.Hypervisor // allocation=vmCPU/vmMem when VM present, "0"/"0" when absent
		hvB           *hv1.Hypervisor
		resA          *v1alpha1.Reservation
		resB          *v1alpha1.Reservation
		expectedHosts []string
		filteredHosts []string
	}{
		{
			// VM fully on hv-a, confirmed in res-a. res-b exists but is empty.
			//
			// hv-a: free=12-4=8cpu. res-a confirmed → block=slot-confirmed=8-4=4 → net=4. Passes 3-cpu req.
			// hv-b: free=12cpu.     res-b no allocs → block=full slot=8         → net=4. Passes.
			name: "Phase 1: VM on hv-a, confirmed in res-a, res-b empty",
			hvA:  newHypervisor("hv-a", hvCapCPU, vmCPU, hvCapMem, vmMem),
			hvB:  newHypervisor("hv-b", hvCapCPU, "0", hvCapMem, "0"),
			resA: newCommittedReservation("res-a", "hv-a", owner, "m1.large", flavorGroup, slotCPU, slotMem,
				crSpecAllocs(crVm("vm-1", vmCPU, vmMem)),
				map[string]string{"vm-1": "hv-a"},
			),
			resB:          newCommittedReservation("res-b", "hv-b", owner, "m1.large", flavorGroup, slotCPU, slotMem, nil, nil),
			expectedHosts: []string{"hv-a", "hv-b"},
			filteredHosts: []string{},
		},
		{
			// Placement pipeline wrote VM into res-b spec. VM is still running on hv-a (not yet migrated).
			//
			// hv-a: free=8, res-a confirmed → block=4   → net=4. Passes.
			// hv-b: free=12, res-b spec-only(4) → remaining=8, specOnly=4, block=max(8,4)=8 → net=4. Passes.
			//
			// res-b blocks its full slot even though VM is only in spec: remaining(8) > specOnly(4).
			name: "Phase 2: VM on hv-a, confirmed in res-a; added to res-b spec only (migration initiated)",
			hvA:  newHypervisor("hv-a", hvCapCPU, vmCPU, hvCapMem, vmMem),
			hvB:  newHypervisor("hv-b", hvCapCPU, "0", hvCapMem, "0"),
			resA: newCommittedReservation("res-a", "hv-a", owner, "m1.large", flavorGroup, slotCPU, slotMem,
				crSpecAllocs(crVm("vm-1", vmCPU, vmMem)),
				map[string]string{"vm-1": "hv-a"},
			),
			resB: newCommittedReservation("res-b", "hv-b", owner, "m1.large", flavorGroup, slotCPU, slotMem,
				crSpecAllocs(crVm("vm-1", vmCPU, vmMem)),
				nil,
			),
			expectedHosts: []string{"hv-a", "hv-b"},
			filteredHosts: []string{},
		},
		{
			// VM appears in both HV allocations during live migration (transient double-presence).
			// res-a: confirmed. res-b: spec-only (controller has not reconciled hv-b yet).
			//
			// hv-a: free=8,  res-a confirmed → block=4   → net=4. Passes.
			// hv-b: free=8,  res-b spec-only → block=8   → net=0. FAILS — conservative until controller confirms.
			name: "Phase 3: VM in both HV allocs (live migration in progress); res-a confirmed, res-b spec-only",
			hvA:  newHypervisor("hv-a", hvCapCPU, vmCPU, hvCapMem, vmMem),
			hvB:  newHypervisor("hv-b", hvCapCPU, vmCPU, hvCapMem, vmMem),
			resA: newCommittedReservation("res-a", "hv-a", owner, "m1.large", flavorGroup, slotCPU, slotMem,
				crSpecAllocs(crVm("vm-1", vmCPU, vmMem)),
				map[string]string{"vm-1": "hv-a"},
			),
			resB: newCommittedReservation("res-b", "hv-b", owner, "m1.large", flavorGroup, slotCPU, slotMem,
				crSpecAllocs(crVm("vm-1", vmCPU, vmMem)),
				nil,
			),
			expectedHosts: []string{"hv-a"},
			filteredHosts: []string{"hv-b"},
		},
		{
			// VM has arrived on hv-b (in alloc) but left hv-a. Controller lag: res-a still confirmed, res-b spec-only.
			//
			// hv-a: free=12 (VM gone), res-a confirmed → block=4 → net=8. Passes.
			// hv-b: free=8,            res-b spec-only → block=8 → net=0. FAILS — res-b not confirmed yet.
			name: "Phase 4: VM arrived on hv-b (in alloc), left hv-a; res-a still confirmed, res-b spec-only",
			hvA:  newHypervisor("hv-a", hvCapCPU, "0", hvCapMem, "0"),
			hvB:  newHypervisor("hv-b", hvCapCPU, vmCPU, hvCapMem, vmMem),
			resA: newCommittedReservation("res-a", "hv-a", owner, "m1.large", flavorGroup, slotCPU, slotMem,
				crSpecAllocs(crVm("vm-1", vmCPU, vmMem)),
				map[string]string{"vm-1": "hv-a"},
			),
			resB: newCommittedReservation("res-b", "hv-b", owner, "m1.large", flavorGroup, slotCPU, slotMem,
				crSpecAllocs(crVm("vm-1", vmCPU, vmMem)),
				nil,
			),
			expectedHosts: []string{"hv-a"},
			filteredHosts: []string{"hv-b"},
		},
		{
			// Controller confirmed VM in res-b. res-a cleanup not done yet (stale confirmed entry).
			//
			// hv-a: free=12 (VM gone), res-a confirmed(stale) → block=8-4=4 → net=8. Passes.
			// hv-b: free=8,            res-b confirmed         → block=8-4=4 → net=4. Passes.
			//
			// hv-a gets its remaining slot capacity back once res-a is cleaned up (phases 6→7).
			name: "Phase 5: VM confirmed in res-b; res-a still has confirmed entry (stale, not yet cleaned)",
			hvA:  newHypervisor("hv-a", hvCapCPU, "0", hvCapMem, "0"),
			hvB:  newHypervisor("hv-b", hvCapCPU, vmCPU, hvCapMem, vmMem),
			resA: newCommittedReservation("res-a", "hv-a", owner, "m1.large", flavorGroup, slotCPU, slotMem,
				crSpecAllocs(crVm("vm-1", vmCPU, vmMem)),
				map[string]string{"vm-1": "hv-a"},
			),
			resB: newCommittedReservation("res-b", "hv-b", owner, "m1.large", flavorGroup, slotCPU, slotMem,
				crSpecAllocs(crVm("vm-1", vmCPU, vmMem)),
				map[string]string{"vm-1": "hv-b"},
			),
			expectedHosts: []string{"hv-a", "hv-b"},
			filteredHosts: []string{},
		},
		{
			// VM removed from res-a Spec.Allocations, but res-a Status.Allocations still stale (one controller cycle lag).
			// spec allocs empty → else branch: res-a blocks its full slot regardless of status.
			// Status allocations are not consulted when spec allocs are absent.
			//
			// hv-a: free=12, res-a spec-empty → block=full slot=8 → net=4. Passes.
			// hv-b: free=8,  res-b confirmed  → block=4           → net=4. Passes.
			name: "Phase 6: VM removed from res-a spec, res-a status stale; res-b confirmed",
			hvA:  newHypervisor("hv-a", hvCapCPU, "0", hvCapMem, "0"),
			hvB:  newHypervisor("hv-b", hvCapCPU, vmCPU, hvCapMem, vmMem),
			resA: newCommittedReservation("res-a", "hv-a", owner, "m1.large", flavorGroup, slotCPU, slotMem,
				nil,                               // spec allocs cleared
				map[string]string{"vm-1": "hv-a"}, // status stale — not consulted when spec is empty
			),
			resB: newCommittedReservation("res-b", "hv-b", owner, "m1.large", flavorGroup, slotCPU, slotMem,
				crSpecAllocs(crVm("vm-1", vmCPU, vmMem)),
				map[string]string{"vm-1": "hv-b"},
			),
			expectedHosts: []string{"hv-a", "hv-b"},
			filteredHosts: []string{},
		},
		{
			// Migration fully complete: res-a is empty, VM confirmed in res-b.
			// Identical blocking to phase 6 for hv-a (spec-empty → full slot block).
			//
			// hv-a: free=12, res-a empty     → block=8 → net=4. Passes.
			// hv-b: free=8,  res-b confirmed → block=4 → net=4. Passes.
			name: "Phase 7: Migration complete — res-a empty, VM confirmed in res-b",
			hvA:  newHypervisor("hv-a", hvCapCPU, "0", hvCapMem, "0"),
			hvB:  newHypervisor("hv-b", hvCapCPU, vmCPU, hvCapMem, vmMem),
			resA: newCommittedReservation("res-a", "hv-a", owner, "m1.large", flavorGroup, slotCPU, slotMem,
				nil, nil,
			),
			resB: newCommittedReservation("res-b", "hv-b", owner, "m1.large", flavorGroup, slotCPU, slotMem,
				crSpecAllocs(crVm("vm-1", vmCPU, vmMem)),
				map[string]string{"vm-1": "hv-b"},
			),
			expectedHosts: []string{"hv-a", "hv-b"},
			filteredHosts: []string{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			objects := []client.Object{tt.hvA, tt.hvB, tt.resA, tt.resB}

			step := &FilterHasEnoughCapacity{}
			step.Client = fake.NewClientBuilder().WithScheme(scheme).WithObjects(objects...).Build()
			step.Options = FilterHasEnoughCapacityOpts{LockReserved: false}

			request := newNovaRequest("instance-new", thirdParty, "m1.small", flavorGroup, 3, "6Gi", false, []string{"hv-a", "hv-b"})
			result, err := step.Run(slog.Default(), request)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			assertActivations(t, result.Activations, tt.expectedHosts, tt.filteredHosts)
		})
	}
}
