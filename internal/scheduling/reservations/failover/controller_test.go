// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package failover

import (
	"testing"

	"github.com/cobaltcore-dev/cortex/api/v1alpha1"
	hv1 "github.com/cobaltcore-dev/openstack-hypervisor-operator/api/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// ============================================================================
// Test: reconcileRemoveNoneligibleVMFromReservations
// ============================================================================

func TestReconcileRemoveNoneligibleVMFromReservations(t *testing.T) {
	tests := []struct {
		name                      string
		vms                       []VM
		reservations              []v1alpha1.Reservation
		expectedUpdatedCount      int
		expectedToUpdateCount     int
		expectedAllocationsPerRes map[string]map[string]string
	}{
		{
			name: "no changes needed - all VMs eligible",
			vms: []VM{
				newTestVMWithResources("vm-1", "host1"),
				newTestVMWithResources("vm-2", "host2"),
			},
			reservations: []v1alpha1.Reservation{
				newTestReservationWithResources("res-1", "host3", map[string]string{
					"vm-1": "host1",
					"vm-2": "host2",
				}),
			},
			expectedUpdatedCount:  1,
			expectedToUpdateCount: 0,
			expectedAllocationsPerRes: map[string]map[string]string{
				"res-1": {"vm-1": "host1", "vm-2": "host2"},
			},
		},
		{
			name: "VM on same host as reservation - remove",
			vms: []VM{
				newTestVMWithResources("vm-1", "host3"), // VM moved to host3 (same as reservation)
			},
			reservations: []v1alpha1.Reservation{
				newTestReservationWithResources("res-1", "host3", map[string]string{
					"vm-1": "host1", // allocation says host1, but VM is now on host3
				}),
			},
			expectedUpdatedCount:  1,
			expectedToUpdateCount: 1,
			expectedAllocationsPerRes: map[string]map[string]string{
				"res-1": {}, // vm-1 removed because it's on the same host as reservation
			},
		},
		{
			name: "multiple ineligible VMs - all processed",
			vms: []VM{
				newTestVMWithResources("vm-1", "host3"), // ineligible (on same host as res-1)
				newTestVMWithResources("vm-2", "host4"), // ineligible (on same host as res-2)
			},
			reservations: []v1alpha1.Reservation{
				newTestReservationWithResources("res-1", "host3", map[string]string{
					"vm-1": "host1",
				}),
				newTestReservationWithResources("res-2", "host4", map[string]string{
					"vm-2": "host2",
				}),
			},
			expectedUpdatedCount:  2,
			expectedToUpdateCount: 2, // Both reservations updated
			expectedAllocationsPerRes: map[string]map[string]string{
				"res-1": {}, // vm-1 removed
				"res-2": {}, // vm-2 removed
			},
		},
		{
			name: "VM not in list - keep in allocations",
			vms: []VM{
				newTestVMWithResources("vm-1", "host1"),
				// vm-2 not in list
			},
			reservations: []v1alpha1.Reservation{
				newTestReservationWithResources("res-1", "host3", map[string]string{
					"vm-1": "host1",
					"vm-2": "host2", // vm-2 not in VMs list - handled by reconcileRemoveInvalidVMFromReservations
				}),
			},
			expectedUpdatedCount:  1,
			expectedToUpdateCount: 0,
			expectedAllocationsPerRes: map[string]map[string]string{
				"res-1": {"vm-1": "host1", "vm-2": "host2"}, // vm-2 kept (not our responsibility)
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			updatedReservations, reservationsToUpdate := reconcileRemoveNoneligibleVMFromReservations(
				tt.vms,
				tt.reservations,
			)

			if len(updatedReservations) != tt.expectedUpdatedCount {
				t.Errorf("expected %d updated reservations, got %d",
					tt.expectedUpdatedCount, len(updatedReservations))
			}

			if len(reservationsToUpdate) != tt.expectedToUpdateCount {
				t.Errorf("expected %d reservations to update, got %d",
					tt.expectedToUpdateCount, len(reservationsToUpdate))
			}

			for _, res := range updatedReservations {
				expectedAllocs, ok := tt.expectedAllocationsPerRes[res.Name]
				if !ok {
					t.Errorf("unexpected reservation %s in result", res.Name)
					continue
				}

				actualAllocs := getAllocations(&res)
				if len(actualAllocs) != len(expectedAllocs) {
					t.Errorf("reservation %s: expected %d allocations, got %d (%v)",
						res.Name, len(expectedAllocs), len(actualAllocs), actualAllocs)
					continue
				}

				for vmUUID, expectedHost := range expectedAllocs {
					actualHost, exists := actualAllocs[vmUUID]
					if !exists {
						t.Errorf("reservation %s: expected VM %s in allocations, but not found",
							res.Name, vmUUID)
						continue
					}
					if actualHost != expectedHost {
						t.Errorf("reservation %s: VM %s expected host %s, got %s",
							res.Name, vmUUID, expectedHost, actualHost)
					}
				}
			}
		})
	}
}

// ============================================================================
// Test: filterFailoverReservations
// ============================================================================

func TestFilterFailoverReservations(t *testing.T) {
	tests := []struct {
		name          string
		reservations  []v1alpha1.Reservation
		expectedCount int
		expectedNames []string
	}{
		{
			name:          "empty list",
			reservations:  []v1alpha1.Reservation{},
			expectedCount: 0,
			expectedNames: nil,
		},
		{
			name: "all failover reservations",
			reservations: []v1alpha1.Reservation{
				{ObjectMeta: metav1.ObjectMeta{Name: "res-1"}, Spec: v1alpha1.ReservationSpec{Type: v1alpha1.ReservationTypeFailover}},
				{ObjectMeta: metav1.ObjectMeta{Name: "res-2"}, Spec: v1alpha1.ReservationSpec{Type: v1alpha1.ReservationTypeFailover}},
			},
			expectedCount: 2,
			expectedNames: []string{"res-1", "res-2"},
		},
		{
			name: "mixed types - only failover returned",
			reservations: []v1alpha1.Reservation{
				{ObjectMeta: metav1.ObjectMeta{Name: "res-1"}, Spec: v1alpha1.ReservationSpec{Type: v1alpha1.ReservationTypeFailover}},
				{ObjectMeta: metav1.ObjectMeta{Name: "res-2"}, Spec: v1alpha1.ReservationSpec{Type: "committed"}},
				{ObjectMeta: metav1.ObjectMeta{Name: "res-3"}, Spec: v1alpha1.ReservationSpec{Type: v1alpha1.ReservationTypeFailover}},
			},
			expectedCount: 2,
			expectedNames: []string{"res-1", "res-3"},
		},
		{
			name: "no failover reservations",
			reservations: []v1alpha1.Reservation{
				{ObjectMeta: metav1.ObjectMeta{Name: "res-1"}, Spec: v1alpha1.ReservationSpec{Type: "committed"}},
			},
			expectedCount: 0,
			expectedNames: nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := filterFailoverReservations(tt.reservations)

			if len(result) != tt.expectedCount {
				t.Errorf("expected %d reservations, got %d", tt.expectedCount, len(result))
			}

			for i, name := range tt.expectedNames {
				if i >= len(result) {
					t.Errorf("missing expected reservation %s", name)
					continue
				}
				if result[i].Name != name {
					t.Errorf("expected reservation %s at index %d, got %s", name, i, result[i].Name)
				}
			}
		})
	}
}

// ============================================================================
// Test: filterVMsOnKnownHypervisors
// ============================================================================

func TestFilterVMsOnKnownHypervisors(t *testing.T) {
	tests := []struct {
		name           string
		vms            []VM
		hypervisorList *hv1.HypervisorList
		expectedCount  int
		expectedUUIDs  []string
	}{
		{
			name: "empty VMs list",
			vms:  []VM{},
			hypervisorList: &hv1.HypervisorList{
				Items: []hv1.Hypervisor{
					newTestHypervisor("host1", []hv1.Instance{}),
					newTestHypervisor("host2", []hv1.Instance{}),
				},
			},
			expectedCount: 0,
			expectedUUIDs: nil,
		},
		{
			name: "all VMs on known hypervisors and in instances",
			vms: []VM{
				newTestVM("vm-1", "host1", "m1.large"),
				newTestVM("vm-2", "host2", "m1.large"),
			},
			hypervisorList: &hv1.HypervisorList{
				Items: []hv1.Hypervisor{
					newTestHypervisor("host1", []hv1.Instance{{ID: "vm-1", Name: "vm-1", Active: true}}),
					newTestHypervisor("host2", []hv1.Instance{{ID: "vm-2", Name: "vm-2", Active: true}}),
					newTestHypervisor("host3", []hv1.Instance{}),
				},
			},
			expectedCount: 2,
			expectedUUIDs: []string{"vm-1", "vm-2"},
		},
		{
			name: "some VMs on unknown hypervisors",
			vms: []VM{
				newTestVM("vm-1", "host1", "m1.large"),
				newTestVM("vm-2", "unknown-host", "m1.large"),
				newTestVM("vm-3", "host2", "m1.large"),
			},
			hypervisorList: &hv1.HypervisorList{
				Items: []hv1.Hypervisor{
					newTestHypervisor("host1", []hv1.Instance{{ID: "vm-1", Name: "vm-1", Active: true}}),
					newTestHypervisor("host2", []hv1.Instance{{ID: "vm-3", Name: "vm-3", Active: true}}),
				},
			},
			expectedCount: 2,
			expectedUUIDs: []string{"vm-1", "vm-3"},
		},
		{
			name: "VM claims hypervisor but not in instances list - filter out",
			vms: []VM{
				newTestVM("vm-1", "host1", "m1.large"),
				newTestVM("vm-2", "host2", "m1.large"), // claims host2 but not in instances
			},
			hypervisorList: &hv1.HypervisorList{
				Items: []hv1.Hypervisor{
					newTestHypervisor("host1", []hv1.Instance{{ID: "vm-1", Name: "vm-1", Active: true}}),
					newTestHypervisor("host2", []hv1.Instance{}), // vm-2 not in instances
				},
			},
			expectedCount: 1,
			expectedUUIDs: []string{"vm-1"},
		},
		{
			name: "VM on wrong hypervisor in instances - filter out",
			vms: []VM{
				newTestVM("vm-1", "host1", "m1.large"), // claims host1
			},
			hypervisorList: &hv1.HypervisorList{
				Items: []hv1.Hypervisor{
					newTestHypervisor("host1", []hv1.Instance{}),                                         // vm-1 not here
					newTestHypervisor("host2", []hv1.Instance{{ID: "vm-1", Name: "vm-1", Active: true}}), // vm-1 is actually here
				},
			},
			expectedCount: 0,
			expectedUUIDs: nil,
		},
		{
			name: "inactive VM in instances - filter out",
			vms: []VM{
				newTestVM("vm-1", "host1", "m1.large"),
			},
			hypervisorList: &hv1.HypervisorList{
				Items: []hv1.Hypervisor{
					newTestHypervisor("host1", []hv1.Instance{{ID: "vm-1", Name: "vm-1", Active: false}}), // inactive
				},
			},
			expectedCount: 0,
			expectedUUIDs: nil,
		},
		{
			name: "no VMs on known hypervisors",
			vms: []VM{
				newTestVM("vm-1", "unknown1", "m1.large"),
				newTestVM("vm-2", "unknown2", "m1.large"),
			},
			hypervisorList: &hv1.HypervisorList{
				Items: []hv1.Hypervisor{
					newTestHypervisor("host1", []hv1.Instance{}),
					newTestHypervisor("host2", []hv1.Instance{}),
				},
			},
			expectedCount: 0,
			expectedUUIDs: nil,
		},
		{
			name: "empty hypervisors list",
			vms: []VM{
				newTestVM("vm-1", "host1", "m1.large"),
			},
			hypervisorList: &hv1.HypervisorList{
				Items: []hv1.Hypervisor{},
			},
			expectedCount: 0,
			expectedUUIDs: nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := filterVMsOnKnownHypervisors(tt.vms, tt.hypervisorList)

			if len(result) != tt.expectedCount {
				t.Errorf("expected %d VMs, got %d", tt.expectedCount, len(result))
			}

			// Check that expected UUIDs are present (order may vary due to filtering)
			resultUUIDs := make(map[string]bool)
			for _, vm := range result {
				resultUUIDs[vm.UUID] = true
			}

			for _, uuid := range tt.expectedUUIDs {
				if !resultUUIDs[uuid] {
					t.Errorf("expected VM %s in result, but not found", uuid)
				}
			}
		})
	}
}

// newTestHypervisor creates a test hypervisor with the given instances
func newTestHypervisor(name string, instances []hv1.Instance) hv1.Hypervisor {
	return hv1.Hypervisor{
		ObjectMeta: metav1.ObjectMeta{
			Name: name,
		},
		Status: hv1.HypervisorStatus{
			Instances: instances,
		},
	}
}

// ============================================================================
// Test: countReservationsForVM
// ============================================================================

func TestCountReservationsForVM(t *testing.T) {
	tests := []struct {
		name          string
		reservations  []v1alpha1.Reservation
		vmUUID        string
		expectedCount int
	}{
		{
			name:          "empty reservations list",
			reservations:  []v1alpha1.Reservation{},
			vmUUID:        "vm-1",
			expectedCount: 0,
		},
		{
			name: "VM in one reservation",
			reservations: []v1alpha1.Reservation{
				newTestReservation("res-1", "host1", map[string]string{"vm-1": "host2"}),
				newTestReservation("res-2", "host3", map[string]string{"vm-2": "host4"}),
			},
			vmUUID:        "vm-1",
			expectedCount: 1,
		},
		{
			name: "VM in multiple reservations",
			reservations: []v1alpha1.Reservation{
				newTestReservation("res-1", "host1", map[string]string{"vm-1": "host2"}),
				newTestReservation("res-2", "host3", map[string]string{"vm-1": "host2", "vm-2": "host4"}),
				newTestReservation("res-3", "host5", map[string]string{"vm-1": "host2"}),
			},
			vmUUID:        "vm-1",
			expectedCount: 3,
		},
		{
			name: "VM not in any reservation",
			reservations: []v1alpha1.Reservation{
				newTestReservation("res-1", "host1", map[string]string{"vm-2": "host2"}),
				newTestReservation("res-2", "host3", map[string]string{"vm-3": "host4"}),
			},
			vmUUID:        "vm-1",
			expectedCount: 0,
		},
		{
			name: "reservation with nil allocations",
			reservations: []v1alpha1.Reservation{
				{
					ObjectMeta: metav1.ObjectMeta{Name: "res-1"},
					Spec:       v1alpha1.ReservationSpec{Type: v1alpha1.ReservationTypeFailover},
					Status:     v1alpha1.ReservationStatus{Host: "host1"},
				},
			},
			vmUUID:        "vm-1",
			expectedCount: 0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := countReservationsForVM(tt.reservations, tt.vmUUID)

			if result != tt.expectedCount {
				t.Errorf("expected count %d, got %d", tt.expectedCount, result)
			}
		})
	}
}

// ============================================================================
// Test: getRequiredFailoverCount
// ============================================================================

func TestGetRequiredFailoverCount(t *testing.T) {
	tests := []struct {
		name          string
		config        FailoverConfig
		flavorName    string
		expectedCount int
	}{
		{
			name: "exact match",
			config: FailoverConfig{
				FlavorFailoverRequirements: map[string]int{
					"m1.large": 2,
				},
			},
			flavorName:    "m1.large",
			expectedCount: 2,
		},
		{
			name: "glob pattern match - prefix",
			config: FailoverConfig{
				FlavorFailoverRequirements: map[string]int{
					"m1.*": 1,
				},
			},
			flavorName:    "m1.large",
			expectedCount: 1,
		},
		{
			name: "pattern match - suffix",
			config: FailoverConfig{
				FlavorFailoverRequirements: map[string]int{
					"*":     1,
					"*hana": 3,
				},
			},
			flavorName:    "m1.hana",
			expectedCount: 3,
		},
		{
			name: "pattern match other sorting - suffix",
			config: FailoverConfig{
				FlavorFailoverRequirements: map[string]int{
					"*hana": 3,
					"*":     1,
				},
			},
			flavorName:    "m1.hana",
			expectedCount: 3,
		},
		{
			name: "glob pattern match - wildcard",
			config: FailoverConfig{
				FlavorFailoverRequirements: map[string]int{
					"*": 1,
				},
			},
			flavorName:    "any-flavor",
			expectedCount: 1,
		},
		{
			name: "no match",
			config: FailoverConfig{
				FlavorFailoverRequirements: map[string]int{
					"m1.large": 2,
				},
			},
			flavorName:    "m2.small",
			expectedCount: 0,
		},
		{
			name: "empty flavor name",
			config: FailoverConfig{
				FlavorFailoverRequirements: map[string]int{
					"*": 1,
				},
			},
			flavorName:    "",
			expectedCount: 0,
		},
		{
			name: "empty requirements",
			config: FailoverConfig{
				FlavorFailoverRequirements: map[string]int{},
			},
			flavorName:    "m1.large",
			expectedCount: 0,
		},
		{
			name: "multiple patterns - highest count wins",
			config: FailoverConfig{
				FlavorFailoverRequirements: map[string]int{
					"m1.large": 5,
					"m1.*":     2,
					"*":        1,
				},
			},
			flavorName:    "m1.large",
			expectedCount: 5, // Note: map iteration order is not guaranteed, but exact match should be found
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			controller := &FailoverReservationController{
				Config: tt.config,
			}

			result := controller.getRequiredFailoverCount(tt.flavorName)

			if result != tt.expectedCount {
				t.Errorf("expected count %d, got %d", tt.expectedCount, result)
			}
		})
	}
}

// ============================================================================
// Test: reconcileRemoveInvalidVMFromReservations
// ============================================================================

func TestReconcileRemoveInvalidVMFromReservations(t *testing.T) {
	tests := []struct {
		name                      string
		vms                       []VM
		reservations              []v1alpha1.Reservation
		expectedUpdatedCount      int // number of reservations in updatedReservations
		expectedToUpdateCount     int // number of reservations that need cluster update
		expectedAllocationsPerRes map[string]map[string]string
	}{
		{
			name: "no changes needed - all VMs valid",
			vms: []VM{
				newTestVM("vm-1", "host1", "flavor1"),
				newTestVM("vm-2", "host2", "flavor1"),
			},
			reservations: []v1alpha1.Reservation{
				newTestReservation("res-1", "host3", map[string]string{
					"vm-1": "host1",
					"vm-2": "host2",
				}),
			},
			expectedUpdatedCount:  1,
			expectedToUpdateCount: 0,
			expectedAllocationsPerRes: map[string]map[string]string{
				"res-1": {"vm-1": "host1", "vm-2": "host2"},
			},
		},
		{
			name: "VM no longer exists - remove from allocations",
			vms: []VM{
				newTestVM("vm-1", "host1", "flavor1"),
				// vm-2 no longer exists
			},
			reservations: []v1alpha1.Reservation{
				newTestReservation("res-1", "host3", map[string]string{
					"vm-1": "host1",
					"vm-2": "host2", // vm-2 should be removed
				}),
			},
			expectedUpdatedCount:  1,
			expectedToUpdateCount: 1,
			expectedAllocationsPerRes: map[string]map[string]string{
				"res-1": {"vm-1": "host1"}, // vm-2 removed
			},
		},
		{
			name: "VM moved to different host - remove from allocations",
			vms: []VM{
				newTestVM("vm-1", "host1", "flavor1"),
				newTestVM("vm-2", "host4", "flavor1"), // moved from host2 to host4
			},
			reservations: []v1alpha1.Reservation{
				newTestReservation("res-1", "host3", map[string]string{
					"vm-1": "host1",
					"vm-2": "host2", // vm-2 moved, should be removed
				}),
			},
			expectedUpdatedCount:  1,
			expectedToUpdateCount: 1,
			expectedAllocationsPerRes: map[string]map[string]string{
				"res-1": {"vm-1": "host1"}, // vm-2 removed because it moved
			},
		},
		{
			name: "multiple reservations - only affected ones updated",
			vms: []VM{
				newTestVM("vm-1", "host1", "flavor1"),
				newTestVM("vm-2", "host2", "flavor1"),
				// vm-3 no longer exists
			},
			reservations: []v1alpha1.Reservation{
				newTestReservation("res-1", "host3", map[string]string{
					"vm-1": "host1",
				}),
				newTestReservation("res-2", "host4", map[string]string{
					"vm-2": "host2",
					"vm-3": "host3", // vm-3 should be removed
				}),
			},
			expectedUpdatedCount:  2,
			expectedToUpdateCount: 1, // only res-2 needs update
			expectedAllocationsPerRes: map[string]map[string]string{
				"res-1": {"vm-1": "host1"},
				"res-2": {"vm-2": "host2"}, // vm-3 removed
			},
		},
		{
			name: "all VMs removed from reservation - empty allocations",
			vms:  []VM{
				// no VMs exist
			},
			reservations: []v1alpha1.Reservation{
				newTestReservation("res-1", "host3", map[string]string{
					"vm-1": "host1",
					"vm-2": "host2",
				}),
			},
			expectedUpdatedCount:  1,
			expectedToUpdateCount: 1,
			expectedAllocationsPerRes: map[string]map[string]string{
				"res-1": {}, // all VMs removed
			},
		},
		{
			name: "empty reservations list",
			vms: []VM{
				newTestVM("vm-1", "host1", "flavor1"),
			},
			reservations:              []v1alpha1.Reservation{},
			expectedUpdatedCount:      0,
			expectedToUpdateCount:     0,
			expectedAllocationsPerRes: map[string]map[string]string{},
		},
		{
			name: "reservation with no allocations - no changes",
			vms: []VM{
				newTestVM("vm-1", "host1", "flavor1"),
			},
			reservations: []v1alpha1.Reservation{
				newTestReservation("res-1", "host3", map[string]string{}),
			},
			expectedUpdatedCount:  1,
			expectedToUpdateCount: 0,
			expectedAllocationsPerRes: map[string]map[string]string{
				"res-1": {},
			},
		},
		{
			name: "mixed scenario - some VMs valid, some deleted, some moved",
			vms: []VM{
				newTestVM("vm-1", "host1", "flavor1"), // valid
				newTestVM("vm-2", "host5", "flavor1"), // moved from host2 to host5
				// vm-3 deleted
				newTestVM("vm-4", "host4", "flavor1"), // valid
			},
			reservations: []v1alpha1.Reservation{
				newTestReservation("res-1", "host6", map[string]string{
					"vm-1": "host1", // valid
					"vm-2": "host2", // moved - remove
				}),
				newTestReservation("res-2", "host7", map[string]string{
					"vm-3": "host3", // deleted - remove
					"vm-4": "host4", // valid
				}),
			},
			expectedUpdatedCount:  2,
			expectedToUpdateCount: 2, // both need update
			expectedAllocationsPerRes: map[string]map[string]string{
				"res-1": {"vm-1": "host1"},
				"res-2": {"vm-4": "host4"},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			updatedReservations, reservationsToUpdate := reconcileRemoveInvalidVMFromReservations(
				tt.vms,
				tt.reservations,
			)

			// Check count of updated reservations
			if len(updatedReservations) != tt.expectedUpdatedCount {
				t.Errorf("expected %d updated reservations, got %d",
					tt.expectedUpdatedCount, len(updatedReservations))
			}

			// Check count of reservations that need cluster update
			if len(reservationsToUpdate) != tt.expectedToUpdateCount {
				t.Errorf("expected %d reservations to update, got %d",
					tt.expectedToUpdateCount, len(reservationsToUpdate))
			}

			// Check allocations for each reservation
			for _, res := range updatedReservations {
				expectedAllocs, ok := tt.expectedAllocationsPerRes[res.Name]
				if !ok {
					t.Errorf("unexpected reservation %s in result", res.Name)
					continue
				}

				actualAllocs := getAllocations(&res)
				if len(actualAllocs) != len(expectedAllocs) {
					t.Errorf("reservation %s: expected %d allocations, got %d",
						res.Name, len(expectedAllocs), len(actualAllocs))
					continue
				}

				for vmUUID, expectedHost := range expectedAllocs {
					actualHost, exists := actualAllocs[vmUUID]
					if !exists {
						t.Errorf("reservation %s: expected VM %s in allocations, but not found",
							res.Name, vmUUID)
						continue
					}
					if actualHost != expectedHost {
						t.Errorf("reservation %s: VM %s expected host %s, got %s",
							res.Name, vmUUID, expectedHost, actualHost)
					}
				}
			}
		})
	}
}

// Test helper functions - local to this test file

func newTestVM(uuid, currentHypervisor, flavorName string) VM {
	return VM{
		UUID:              uuid,
		CurrentHypervisor: currentHypervisor,
		FlavorName:        flavorName,
		ProjectID:         "test-project",
	}
}

func newTestVMWithResources(uuid, currentHypervisor string) VM {
	return VM{
		UUID:              uuid,
		CurrentHypervisor: currentHypervisor,
		FlavorName:        "m1.large",
		ProjectID:         "test-project",
		Resources: map[string]resource.Quantity{
			"memory": *resource.NewQuantity(8192*1024*1024, resource.BinarySI),
			"vcpus":  *resource.NewQuantity(4, resource.DecimalSI),
		},
	}
}

func newTestReservation(name, host string, allocations map[string]string) v1alpha1.Reservation {
	return v1alpha1.Reservation{
		ObjectMeta: metav1.ObjectMeta{
			Name: name,
		},
		Spec: v1alpha1.ReservationSpec{
			Type: v1alpha1.ReservationTypeFailover,
		},
		Status: v1alpha1.ReservationStatus{
			Host: host,
			FailoverReservation: &v1alpha1.FailoverReservationStatus{
				Allocations: allocations,
			},
		},
	}
}

func newTestReservationWithResources(name, host string, allocations map[string]string) v1alpha1.Reservation {
	return v1alpha1.Reservation{
		ObjectMeta: metav1.ObjectMeta{
			Name: name,
		},
		Spec: v1alpha1.ReservationSpec{
			Type:       v1alpha1.ReservationTypeFailover,
			TargetHost: host,
			Resources: map[hv1.ResourceName]resource.Quantity{
				"memory": *resource.NewQuantity(8192*1024*1024, resource.BinarySI),
				"cpu":    *resource.NewQuantity(4, resource.DecimalSI),
			},
		},
		Status: v1alpha1.ReservationStatus{
			Host: host,
			FailoverReservation: &v1alpha1.FailoverReservationStatus{
				Allocations: allocations,
			},
		},
	}
}

func getAllocations(res *v1alpha1.Reservation) map[string]string {
	if res.Status.FailoverReservation == nil {
		return nil
	}
	return res.Status.FailoverReservation.Allocations
}

// ============================================================================
// Test: selectVMsToProcess
// ============================================================================

func TestSelectVMsToProcess(t *testing.T) {
	// Create 10 VMs with different memory sizes (sorted by memory descending)
	createVMs := func(count int) []vmFailoverNeed {
		vms := make([]vmFailoverNeed, count)
		for i := range count {
			vms[i] = vmFailoverNeed{
				VM: VM{
					UUID:              "vm-" + string(rune('a'+i)),
					CurrentHypervisor: "host" + string(rune('1'+i)),
					Resources: map[string]resource.Quantity{
						"memory": *resource.NewQuantity(int64((count-i)*1024*1024*1024), resource.BinarySI), // Descending memory
					},
				},
				Count: 1,
			}
		}
		return vms
	}

	tests := []struct {
		name           string
		reconcileCount int64
		vmCount        int
		maxToProcess   int
		expectedOffset int // Expected starting offset in the VM list
		expectedHit    bool
	}{
		// 3 out of 4 runs should start at offset 0
		{
			name:           "reconcile 1 - offset 0",
			reconcileCount: 1,
			vmCount:        10,
			maxToProcess:   3,
			expectedOffset: 0,
			expectedHit:    true,
		},
		{
			name:           "reconcile 2 - offset 0",
			reconcileCount: 2,
			vmCount:        10,
			maxToProcess:   3,
			expectedOffset: 0,
			expectedHit:    true,
		},
		{
			name:           "reconcile 3 - offset 0",
			reconcileCount: 3,
			vmCount:        10,
			maxToProcess:   3,
			expectedOffset: 0,
			expectedHit:    true,
		},
		// Every 4th reconcile uses reconcileCount as offset (mod vmCount)
		{
			name:           "reconcile 4 - offset 4",
			reconcileCount: 4,
			vmCount:        10,
			maxToProcess:   3,
			expectedOffset: 4,
			expectedHit:    true,
		},
		{
			name:           "reconcile 5 - offset 0",
			reconcileCount: 5,
			vmCount:        10,
			maxToProcess:   3,
			expectedOffset: 0,
			expectedHit:    true,
		},
		{
			name:           "reconcile 6 - offset 0",
			reconcileCount: 6,
			vmCount:        10,
			maxToProcess:   3,
			expectedOffset: 0,
			expectedHit:    true,
		},
		{
			name:           "reconcile 7 - offset 0",
			reconcileCount: 7,
			vmCount:        10,
			maxToProcess:   3,
			expectedOffset: 0,
			expectedHit:    true,
		},
		{
			name:           "reconcile 8 - offset 8",
			reconcileCount: 8,
			vmCount:        10,
			maxToProcess:   3,
			expectedOffset: 8,
			expectedHit:    true,
		},
		// Test wrap-around when reconcileCount > vmCount
		{
			name:           "reconcile 12 - offset 2 (12 mod 10)",
			reconcileCount: 12,
			vmCount:        10,
			maxToProcess:   3,
			expectedOffset: 2, // 12 % 10 = 2
			expectedHit:    true,
		},
		{
			name:           "reconcile 20 - offset 0 (20 mod 10)",
			reconcileCount: 20,
			vmCount:        10,
			maxToProcess:   3,
			expectedOffset: 0, // 20 % 10 = 0
			expectedHit:    true,
		},
		// Edge cases
		{
			name:           "maxToProcess 0 - no limit, returns all",
			reconcileCount: 4,
			vmCount:        10,
			maxToProcess:   0,
			expectedOffset: 0, // No limit means all VMs returned starting from 0
			expectedHit:    false,
		},
		{
			name:           "maxToProcess >= vmCount - no limit hit",
			reconcileCount: 4,
			vmCount:        5,
			maxToProcess:   10,
			expectedOffset: 0, // All VMs fit, no rotation needed
			expectedHit:    false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			controller := &FailoverReservationController{
				reconcileCount: tt.reconcileCount,
			}

			vms := createVMs(tt.vmCount)
			selected, hitLimit := controller.selectVMsToProcess(vms, tt.maxToProcess)

			if hitLimit != tt.expectedHit {
				t.Errorf("expected hitLimit=%v, got %v", tt.expectedHit, hitLimit)
			}

			if !tt.expectedHit {
				// When no limit is hit, all VMs should be returned
				if len(selected) != tt.vmCount {
					t.Errorf("expected all %d VMs when no limit hit, got %d", tt.vmCount, len(selected))
				}
				return
			}

			// Verify the first selected VM is at the expected offset
			if len(selected) == 0 {
				t.Error("expected at least one VM selected")
				return
			}

			// The VMs are sorted by memory descending, so vm-a has most memory, vm-j has least
			// After sorting, the order is: vm-a, vm-b, vm-c, ..., vm-j
			// With offset, we should start at vms[offset]
			expectedFirstVM := vms[tt.expectedOffset].VM.UUID
			actualFirstVM := selected[0].VM.UUID

			if actualFirstVM != expectedFirstVM {
				t.Errorf("expected first VM to be %s (offset %d), got %s",
					expectedFirstVM, tt.expectedOffset, actualFirstVM)
			}

			// Verify we got the expected number of VMs
			expectedCount := tt.maxToProcess
			if expectedCount > tt.vmCount {
				expectedCount = tt.vmCount
			}
			if len(selected) != expectedCount {
				t.Errorf("expected %d VMs selected, got %d", expectedCount, len(selected))
			}
		})
	}
}
