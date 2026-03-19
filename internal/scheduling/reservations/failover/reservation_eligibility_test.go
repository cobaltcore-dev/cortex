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

// Default resources for test VMs and reservations (4GB memory, 2 vcpus)
// Note: Reservations use "cpu" as the canonical key, VMs use "vcpus"
var defaultResources = map[hv1.ResourceName]resource.Quantity{
	"memory": resource.MustParse("4Gi"),
	"cpu":    resource.MustParse("2"),
}

var defaultVMResources = map[string]resource.Quantity{
	"memory": resource.MustParse("4Gi"),
	"vcpus":  resource.MustParse("2"),
}

// makeReservation creates a test reservation with the given parameters.
func makeReservation(name, host string, usedBy map[string]string) v1alpha1.Reservation {
	return v1alpha1.Reservation{
		ObjectMeta: metav1.ObjectMeta{Name: name},
		Spec: v1alpha1.ReservationSpec{
			Type:       v1alpha1.ReservationTypeFailover,
			TargetHost: host,
			Resources:  defaultResources,
		},
		Status: v1alpha1.ReservationStatus{
			Host: host,
			FailoverReservation: &v1alpha1.FailoverReservationStatus{
				Allocations: usedBy,
			},
		},
	}
}

// makeReservationWithResources creates a test reservation with custom resources.
func makeReservationWithResources(name, host string, usedBy map[string]string, resources map[hv1.ResourceName]resource.Quantity) v1alpha1.Reservation { //nolint:unparam // name is always "res-1" in tests but kept for clarity
	return v1alpha1.Reservation{
		ObjectMeta: metav1.ObjectMeta{Name: name},
		Spec: v1alpha1.ReservationSpec{
			Type:       v1alpha1.ReservationTypeFailover,
			TargetHost: host,
			Resources:  resources,
		},
		Status: v1alpha1.ReservationStatus{
			Host: host,
			FailoverReservation: &v1alpha1.FailoverReservationStatus{
				Allocations: usedBy,
			},
		},
	}
}

// makeVM creates a test VM with the given parameters.
func makeVM(uuid, hypervisor string) VM {
	return VM{
		UUID:              uuid,
		CurrentHypervisor: hypervisor,
		Resources:         defaultVMResources,
	}
}

// makeVMWithResources creates a test VM with custom resources.
func makeVMWithResources(uuid, hypervisor string, resources map[string]resource.Quantity) VM { //nolint:unparam // uuid is always "vm-1" in tests but kept for clarity
	return VM{
		UUID:              uuid,
		CurrentHypervisor: hypervisor,
		Resources:         resources,
	}
}

// buildVMHypervisorsMap builds a map of VM UUID to their hypervisors from failover reservations.
// It also includes the VM we are checking (vm) with its current hypervisor,
// and the candidate reservation (which may have VMs not in allFailoverReservations).
// This is a test helper function used to verify data structure consistency.
func buildVMHypervisorsMap(vm VM, candidateReservation v1alpha1.Reservation, allFailoverReservations []v1alpha1.Reservation) map[string]map[string]bool {
	vmHypervisorsMap := make(map[string]map[string]bool)

	vmHypervisorsMap[vm.UUID] = make(map[string]bool)
	vmHypervisorsMap[vm.UUID][vm.CurrentHypervisor] = true

	// Add VMs from reservation allocations
	for _, res := range allFailoverReservations {
		allocations := getFailoverAllocations(&res)
		for vmUUID, vmHypervisor := range allocations {
			if vmHypervisorsMap[vmUUID] == nil {
				vmHypervisorsMap[vmUUID] = make(map[string]bool)
			}
			vmHypervisorsMap[vmUUID][vmHypervisor] = true
		}
	}

	// Add VMs from the candidate reservation
	candidateAllocations := getFailoverAllocations(&candidateReservation)
	for vmUUID, vmHypervisor := range candidateAllocations {
		if vmHypervisorsMap[vmUUID] == nil {
			vmHypervisorsMap[vmUUID] = make(map[string]bool)
		}
		vmHypervisorsMap[vmUUID][vmHypervisor] = true
	}

	return vmHypervisorsMap
}

// TestIsVMEligibleForReservation tests the IsVMEligibleForReservation function with various scenarios.
func TestIsVMEligibleForReservation(t *testing.T) {
	testCases := []struct {
		name            string
		vm              VM
		reservation     v1alpha1.Reservation
		vmHostMap       map[string]string
		allReservations []v1alpha1.Reservation
		expected        bool
	}{
		// ============================================================================
		// Basic eligibility tests
		// ============================================================================
		{
			name:        "eligible: empty reservation on different host",
			vm:          makeVM("vm-1", "host1"),
			reservation: makeReservation("res-1", "host2", map[string]string{}),
			vmHostMap: map[string]string{
				"vm-1": "host1",
			},
			allReservations: []v1alpha1.Reservation{},
			expected:        true,
		},
		{
			name:        "eligible: reservation not in allReservations list",
			vm:          makeVM("vm-1", "host1"),
			reservation: makeReservation("res-new", "host2", map[string]string{}),
			vmHostMap: map[string]string{
				"vm-1": "host1",
			},
			allReservations: []v1alpha1.Reservation{
				makeReservation("res-other", "host3", map[string]string{"vm-2": "host2"}),
			},
			expected: true,
		},
		{
			name:        "eligible: empty allReservations with non-empty candidate",
			vm:          makeVM("vm-2", "host2"),
			reservation: makeReservation("res-1", "host3", map[string]string{"vm-1": "host1"}),
			vmHostMap: map[string]string{
				"vm-1": "host1",
				"vm-2": "host2",
			},
			allReservations: []v1alpha1.Reservation{},
			expected:        true,
		},
		{
			name:        "ineligible: VM already uses this reservation",
			vm:          makeVM("vm-1", "host1"),
			reservation: makeReservation("res-1", "host2", map[string]string{"vm-1": "host1"}),
			vmHostMap: map[string]string{
				"vm-1": "host1",
			},
			allReservations: []v1alpha1.Reservation{},
			expected:        false,
		},
		// ============================================================================
		// Constraint 1: VM cannot reserve on its own host
		// ============================================================================
		{
			name:        "C1: ineligible - reservation on VM's own host",
			vm:          makeVM("vm-1", "host1"),
			reservation: makeReservation("res-1", "host1", map[string]string{}),
			vmHostMap: map[string]string{
				"vm-1": "host1",
			},
			allReservations: []v1alpha1.Reservation{},
			expected:        false,
		},
		{
			name:        "C1: ineligible - reservation on VM's own host with other VMs",
			vm:          makeVM("vm-1", "host1"),
			reservation: makeReservation("res-1", "host1", map[string]string{"vm-2": "host2"}),
			vmHostMap: map[string]string{
				"vm-1": "host1",
				"vm-2": "host2",
			},
			allReservations: []v1alpha1.Reservation{},
			expected:        false,
		},
		// ============================================================================
		// Constraint 2: VM's reservations must be on distinct hosts
		// ============================================================================
		{
			name:        "C2: ineligible - VM already has reservation on same host",
			vm:          makeVM("vm-1", "host1"),
			reservation: makeReservation("res-2", "host3", map[string]string{}),
			vmHostMap: map[string]string{
				"vm-1": "host1",
			},
			allReservations: []v1alpha1.Reservation{
				makeReservation("res-1", "host3", map[string]string{"vm-1": "host1"}),
			},
			expected: false,
		},
		{
			name:        "C2: eligible - VM has reservations on different hosts",
			vm:          makeVM("vm-1", "host1"),
			reservation: makeReservation("res-2", "host4", map[string]string{}),
			vmHostMap: map[string]string{
				"vm-1": "host1",
			},
			allReservations: []v1alpha1.Reservation{
				makeReservation("res-1", "host3", map[string]string{"vm-1": "host1"}),
			},
			expected: true,
		},
		{
			name:        "C2: ineligible - VM has 2 reservations, third would be on duplicate host",
			vm:          makeVM("vm-1", "host1"),
			reservation: makeReservation("res-3", "host3", map[string]string{}),
			vmHostMap: map[string]string{
				"vm-1": "host1",
			},
			allReservations: []v1alpha1.Reservation{
				makeReservation("res-1", "host3", map[string]string{"vm-1": "host1"}),
				makeReservation("res-2", "host4", map[string]string{"vm-1": "host1"}),
			},
			expected: false,
		},
		{
			name:        "C2: eligible - VM has 2 reservations on different hosts, third on new host",
			vm:          makeVM("vm-1", "host1"),
			reservation: makeReservation("res-3", "host5", map[string]string{}),
			vmHostMap: map[string]string{
				"vm-1": "host1",
			},
			allReservations: []v1alpha1.Reservation{
				makeReservation("res-1", "host3", map[string]string{"vm-1": "host1"}),
				makeReservation("res-2", "host4", map[string]string{"vm-1": "host1"}),
			},
			expected: true,
		},
		{
			name:        "C3: eligible - VM can share reservation with VM on different host",
			vm:          makeVM("vm-3", "host3"),
			reservation: makeReservation("res-1", "host2", map[string]string{"vm-1": "host1"}),
			vmHostMap: map[string]string{
				"vm-1": "host1",
				"vm-2": "host2",
				"vm-3": "host3",
			},
			allReservations: []v1alpha1.Reservation{
				makeReservation("res-1", "host2", map[string]string{"vm-1": "host1"}),
			},
			expected: true,
		},
		// ============================================================================
		// Constraint 3 extended: VMs cannot share if one has reservation on other's host
		// ============================================================================
		{
			name:        "C3ext: ineligible - VM has reservation on other VM's host",
			vm:          makeVM("vm-3", "host3"),
			reservation: makeReservation("res-2", "host5", map[string]string{"vm-1": "host1"}),
			vmHostMap: map[string]string{
				"vm-1": "host1",
				"vm-2": "host2",
				"vm-3": "host3",
				"vm-4": "host4",
			},
			allReservations: []v1alpha1.Reservation{
				makeReservation("res-1", "host1", map[string]string{"vm-3": "host3"}),
				makeReservation("res-2", "host5", map[string]string{"vm-1": "host1"}),
			},
			expected: false,
		},
		// ============================================================================
		// Constraint 4: VMs using shared reservation cannot run on VM's reservation hosts
		// ============================================================================
		{
			name:        "C4: ineligible - reservation user runs on VM's reservation host",
			vm:          makeVM("vm-1", "host1"),
			reservation: makeReservation("res-2", "host3", map[string]string{"vm-2": "host2"}),
			vmHostMap: map[string]string{
				"vm-1": "host1",
				"vm-2": "host2",
			},
			allReservations: []v1alpha1.Reservation{
				makeReservation("res-1", "host2", map[string]string{"vm-1": "host1"}),
			},
			expected: false,
		},
		{
			name:        "C4: ineligible - vm_b runs on vm_a's reservation host",
			vm:          makeVM("vm_a", "host1"),
			reservation: makeReservation("res_k", "host3", map[string]string{"vm_b": "host2"}),
			vmHostMap: map[string]string{
				"vm_a": "host1",
				"vm_b": "host2",
			},
			allReservations: []v1alpha1.Reservation{
				makeReservation("res_l", "host2", map[string]string{"vm_a": "host1"}),
				makeReservation("res_k", "host3", map[string]string{"vm_b": "host2"}),
				makeReservation("res_m", "host4", map[string]string{"vm_b": "host2"}),
			},
			expected: false,
		},
		// ============================================================================
		// Constraint 5: No two VMs (other than v) using v's slots can have same host
		// For VM v with slots R = {r1..rn}, there exist no vm_j, vm_k (vm_j != v and vm_k != v)
		// with vm_j uses r_j and vm_k uses r_k and host(vm_j) = host(vm_k).
		// Note: vm_j and vm_k CAN be the same VM (same VM using multiple slots violates this)
		// ============================================================================
		{
			name:        "C5: ineligible - two different VMs using v's slots on same host",
			vm:          makeVM("vm-1", "host1"),
			reservation: makeReservation("res-2", "host4", map[string]string{"vm-3": "host2"}),
			vmHostMap: map[string]string{
				"vm-1": "host1",
				"vm-2": "host2",
				"vm-3": "host2",
			},
			allReservations: []v1alpha1.Reservation{
				makeReservation("res-1", "host3", map[string]string{"vm-1": "host1", "vm-2": "host2"}),
			},
			expected: false,
		},
		{
			name:        "C5: ineligible - vm_b and vm_c both use vm_a's slots and are on same host",
			vm:          makeVM("vm_a", "host1"),
			reservation: makeReservation("res_k", "host2", map[string]string{"vm_b": "host4"}),
			vmHostMap: map[string]string{
				"vm_a": "host1",
				"vm_b": "host4",
				"vm_c": "host4",
			},
			allReservations: []v1alpha1.Reservation{
				makeReservation("res_m", "host1", map[string]string{"vm_b": "host4"}),
				makeReservation("res_n", "host1", map[string]string{"vm_c": "host4"}),
				makeReservation("res_k", "host2", map[string]string{"vm_b": "host4"}),
				makeReservation("res_l", "host3", map[string]string{"vm_a": "host1", "vm_c": "host4"}),
			},
			expected: false,
		},
		{
			name: "C5: ineligible - vm-1 would use multiple of vm-2's slots",
			vm:   makeVM("vm-2", "host2"),
			reservation: makeReservation("res-5", "host5", map[string]string{
				"vm-1": "host1",
			}),
			vmHostMap: map[string]string{
				"vm-1": "host1",
				"vm-2": "host2",
				"vm-3": "host3",
				"vm-4": "host4",
			},
			allReservations: []v1alpha1.Reservation{
				makeReservation("res-4", "host4", map[string]string{
					"vm-1": "host1",
					"vm-2": "host2",
					"vm-3": "host3",
				}),
				makeReservation("res-5", "host5", map[string]string{
					"vm-1": "host1",
				}),
			},
			// vm-1 would use both res-4 and res-5 (two of vm-2's slots)
			// vm_j = vm-1 uses res-4, vm_k = vm-1 uses res-5, host(vm_j) = host(vm_k) = host1 → VIOLATION
			expected: false,
		},
		{
			name: "C5: ineligible - vm-1 would use both res-3 and res-4 (vm-2's slots)",
			vm:   makeVM("vm-2", "host2"),
			reservation: makeReservation("res-4", "host4", map[string]string{
				"vm-1": "host1",
			}),
			vmHostMap: map[string]string{
				"vm-1": "host1",
				"vm-2": "host2",
			},
			allReservations: []v1alpha1.Reservation{
				makeReservation("res-3", "host3", map[string]string{
					"vm-1": "host1",
					"vm-2": "host2",
				}),
				makeReservation("res-4", "host4", map[string]string{
					"vm-1": "host1",
				}),
			},
			// vm-1 would use both res-3 and res-4 (two of vm-2's slots)
			// vm_j = vm-1 uses res-3, vm_k = vm-1 uses res-4, host(vm_j) = host(vm_k) = host1 → VIOLATION
			expected: false,
		},
		{
			name: "C5: eligible - vm-1 only uses one of vm-2's slots",
			vm:   makeVM("vm-2", "host2"),
			reservation: makeReservation("res-4", "host4", map[string]string{
				"vm-1": "host1",
			}),
			vmHostMap: map[string]string{
				"vm-1": "host1",
				"vm-2": "host2",
			},
			allReservations: []v1alpha1.Reservation{
				makeReservation("res-3", "host3", map[string]string{
					"vm-1": "host1",
				}),
				makeReservation("res-4", "host4", map[string]string{
					"vm-1": "host1",
				}),
				makeReservation("res-5", "host5", map[string]string{
					"vm-2": "host2",
				}),
			},
			expected: true,
		},
		{
			name: "C5: ineligible - vm-1 and vm-3 both use vm-2's slots and vm-1 is on host1",
			vm:   makeVM("vm-2", "host2"),
			reservation: makeReservation("res-1", "host1", map[string]string{
				"vm-3": "host3",
			}),
			vmHostMap: map[string]string{
				"vm-1": "host1",
				"vm-2": "host2",
				"vm-3": "host3",
				"vm-4": "host4",
			},
			allReservations: []v1alpha1.Reservation{
				makeReservation("res-3", "host3", map[string]string{
					"vm-1": "host1",
					"vm-2": "host2",
				}),
				makeReservation("res-6", "host6", map[string]string{
					"vm-1": "host1",
					"vm-3": "host3",
					"vm-4": "host4",
				}),
				makeReservation("res-1", "host1", map[string]string{
					"vm-3": "host3",
				}),
			},
			expected: false,
		},
		// ============================================================================
		// Constraint 3: VMs sharing a reservation cannot be on the same host
		// ============================================================================
		{
			name:        "C3: ineligible - another VM on same host uses reservation",
			vm:          makeVM("vm-1", "host1"),
			reservation: makeReservation("res-1", "host3", map[string]string{"vm-2": "host1"}),
			vmHostMap: map[string]string{
				"vm-1": "host1",
				"vm-2": "host1",
			},
			allReservations: []v1alpha1.Reservation{},
			expected:        false,
		},
		{
			name:        "C3: eligible - other VMs on different hosts",
			vm:          makeVM("vm-1", "host1"),
			reservation: makeReservation("res-1", "host3", map[string]string{"vm-2": "host2"}),
			vmHostMap: map[string]string{
				"vm-1": "host1",
				"vm-2": "host2",
			},
			allReservations: []v1alpha1.Reservation{},
			expected:        true,
		},
		{
			// vm-2 wants to use res-1 (empty) on host1. If vm-2 uses res-1:
			// - vm-2's slots: res-3 on host3 (existing), res-1 on host1 (candidate)
			// - vm-2's slot hosts: {host3, host1}
			// - VMs using vm-2's slots: vm-1 uses res-3 (on host1)
			// - Constraint 4: vm-1 is on host1, which is in vm-2's slot hosts → VIOLATION!
			name:        "C4: ineligible - vm-1 uses vm-2's slot and runs on candidate reservation's host (empty res)",
			vm:          makeVM("vm-2", "host2"),
			reservation: makeReservation("res-1", "host1", map[string]string{}),
			vmHostMap: map[string]string{
				"vm-1": "host1",
				"vm-2": "host2",
				"vm-3": "host3",
				"vm-4": "host4",
			},
			allReservations: []v1alpha1.Reservation{
				makeReservation("res-3", "host3", map[string]string{
					"vm-1": "host1",
					"vm-2": "host2",
				}),
				makeReservation("res-4", "host4", map[string]string{
					"vm-1": "host1",
				}),
				makeReservation("res-1", "host1", map[string]string{}),
			},
			expected: false,
		},
		// ============================================================================
		// Integration test scenario: vm-3 should be able to use reservation on host1
		// ============================================================================
		{
			// Scenario from integration test:
			// - host1 and host3 failed
			// - vm-1 on host1, vm-3 on host3 need evacuation
			// - existing-res-1 on host4 has: vm-1, vm-3
			// - existing-res-2 on host5 has: vm-1, vm-2
			// - failover-zxmbh on host1 has: vm-2, vm-3
			// should be able to use failover-zxmbh on host1 (but host1 failed, so this is moot)
			// vm-3 should be able to use existing-res-1 on host4
			// But vm-1 is also using existing-res-1, and both are evacuating
			// This test checks if vm-3 can use the reservation on host1 when vm-3 is NOT yet in it
			name:        "integration: vm-3 ineligible for reservation on host1 (constraint violation)",
			vm:          makeVM("vm-3", "host3"),
			reservation: makeReservation("failover-zxmbh", "host1", map[string]string{"vm-2": "host2"}),
			vmHostMap: map[string]string{
				"vm-1": "host1",
				"vm-2": "host2",
				"vm-3": "host3",
			},
			allReservations: []v1alpha1.Reservation{
				makeReservation("existing-res-1", "host4", map[string]string{"vm-1": "host1", "vm-3": "host3"}),
				makeReservation("existing-res-2", "host5", map[string]string{"vm-1": "host1", "vm-2": "host2"}),
				makeReservation("failover-zxmbh", "host1", map[string]string{"vm-2": "host2"}),
			},
			// vm-3 wants to use failover-zxmbh on host1
			// Constraint 1: host1 != host3 ✓
			// Constraint 2: vm-3 already has reservations on host4, not host1 ✓
			// Constraint 3: vm-2 uses failover-zxmbh, vm-2 is on host2, vm-3 is on host3 ✓
			// Constraint 4: vm-2 (using failover-zxmbh) is on host2, vm-3's reservation hosts are [host4]
			//               vm-2 is not on host3 (vm-3's host) ✓
			//               vm-2 is not on host4 (vm-3's reservation host) ✓
			// Constraint 5: VMs using vm-3's slots (existing-res-1, failover-zxmbh):
			//               existing-res-1: vm-1 on host1
			//               failover-zxmbh: vm-2 on host2
			//               vm-1 and vm-2 are on different hosts ✓
			expected: false,
		},
		// ============================================================================
		// Circular dependency scenarios
		// ============================================================================
		{
			name: "circular: ineligible - vm-3 has res on vm-1's host, vm-1 has res on vm-3's host",
			vm:   makeVM("vm-3", "host3"),
			reservation: makeReservation("res-2", "host2", map[string]string{
				"vm-1": "host1",
			}),
			vmHostMap: map[string]string{
				"vm-1": "host1",
				"vm-2": "host2",
				"vm-3": "host3",
				"vm-4": "host4",
			},
			allReservations: []v1alpha1.Reservation{
				makeReservation("res-1", "host1", map[string]string{
					"vm-3": "host3",
				}),
				makeReservation("res-3", "host3", map[string]string{
					"vm-1": "host1",
					"vm-2": "host2",
					"vm-4": "host4",
				}),
				makeReservation("res-2", "host2", map[string]string{
					"vm-1": "host1",
				}),
			},
			expected: false,
		},
		{
			name: "circular: ineligible - vm-3 has res on vm-1's host, wants to share with vm-1",
			vm:   makeVM("vm-3", "host3"),
			reservation: makeReservation("res-2", "host2", map[string]string{
				"vm-1": "host1",
				"vm-4": "host4",
			}),
			vmHostMap: map[string]string{
				"vm-1": "host1",
				"vm-2": "host2",
				"vm-3": "host3",
				"vm-4": "host4",
			},
			allReservations: []v1alpha1.Reservation{
				makeReservation("res-1", "host1", map[string]string{
					"vm-3": "host3",
				}),
				makeReservation("res-2", "host2", map[string]string{
					"vm-1": "host1",
					"vm-4": "host4",
				}),
				makeReservation("res-6", "host6", map[string]string{
					"vm-1": "host1",
					"vm-2": "host2",
				}),
			},
			expected: false,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			// The new API builds VMHostsMap from the VM and allReservations
			// No need to add temp reservations - the VM's host is included automatically
			result := IsVMEligibleForReservation(tc.vm, tc.reservation, tc.allReservations)

			if result != tc.expected {
				t.Errorf("IsVMEligibleForReservation() = %v, expected %v", result, tc.expected)
			}
		})
	}
}

// TestDoesVMFitInReservation tests the doesVMFitInReservation function.
func TestDoesVMFitInReservation(t *testing.T) {
	testCases := []struct {
		name        string
		vm          VM
		reservation v1alpha1.Reservation
		expected    bool
	}{
		{
			name: "fits: VM fits exactly in reservation",
			vm: makeVMWithResources("vm-1", "host1", map[string]resource.Quantity{
				"memory": resource.MustParse("4Gi"),
				"vcpus":  resource.MustParse("2"),
			}),
			reservation: makeReservationWithResources("res-1", "host2", map[string]string{}, map[hv1.ResourceName]resource.Quantity{
				"memory": resource.MustParse("4Gi"),
				"cpu":    resource.MustParse("2"),
			}),
			expected: true,
		},
		{
			name: "fits: VM is smaller than reservation",
			vm: makeVMWithResources("vm-1", "host1", map[string]resource.Quantity{
				"memory": resource.MustParse("2Gi"),
				"vcpus":  resource.MustParse("1"),
			}),
			reservation: makeReservationWithResources("res-1", "host2", map[string]string{}, map[hv1.ResourceName]resource.Quantity{
				"memory": resource.MustParse("4Gi"),
				"cpu":    resource.MustParse("2"),
			}),
			expected: true,
		},
		{
			name: "exceeds: VM memory exceeds reservation",
			vm: makeVMWithResources("vm-1", "host1", map[string]resource.Quantity{
				"memory": resource.MustParse("8Gi"),
				"vcpus":  resource.MustParse("2"),
			}),
			reservation: makeReservationWithResources("res-1", "host2", map[string]string{}, map[hv1.ResourceName]resource.Quantity{
				"memory": resource.MustParse("4Gi"),
				"cpu":    resource.MustParse("2"),
			}),
			expected: false,
		},
		{
			name: "exceeds: VM vcpus exceeds reservation cpu",
			vm: makeVMWithResources("vm-1", "host1", map[string]resource.Quantity{
				"memory": resource.MustParse("4Gi"),
				"vcpus":  resource.MustParse("4"),
			}),
			reservation: makeReservationWithResources("res-1", "host2", map[string]string{}, map[hv1.ResourceName]resource.Quantity{
				"memory": resource.MustParse("4Gi"),
				"cpu":    resource.MustParse("2"),
			}),
			expected: false,
		},
		{
			name: "fits: VM has no resources defined",
			vm:   makeVMWithResources("vm-1", "host1", map[string]resource.Quantity{}),
			reservation: makeReservationWithResources("res-1", "host2", map[string]string{}, map[hv1.ResourceName]resource.Quantity{
				"memory": resource.MustParse("4Gi"),
				"cpu":    resource.MustParse("2"),
			}),
			expected: true,
		},
		{
			name: "exceeds: reservation has no memory resource",
			vm: makeVMWithResources("vm-1", "host1", map[string]resource.Quantity{
				"memory": resource.MustParse("4Gi"),
				"vcpus":  resource.MustParse("2"),
			}),
			reservation: makeReservationWithResources("res-1", "host2", map[string]string{}, map[hv1.ResourceName]resource.Quantity{
				"cpu": resource.MustParse("2"),
			}),
			expected: false,
		},
		{
			name: "exceeds: reservation has no cpu resource",
			vm: makeVMWithResources("vm-1", "host1", map[string]resource.Quantity{
				"memory": resource.MustParse("4Gi"),
				"vcpus":  resource.MustParse("2"),
			}),
			reservation: makeReservationWithResources("res-1", "host2", map[string]string{}, map[hv1.ResourceName]resource.Quantity{
				"memory": resource.MustParse("4Gi"),
			}),
			expected: false,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			result := doesVMFitInReservation(tc.vm, tc.reservation)

			if result != tc.expected {
				t.Errorf("doesVMFitInReservation() = %v, expected %v", result, tc.expected)
			}
		})
	}
}

// updateReservationInList returns a new slice with the given reservation updated.
func updateReservationInList(reservations []v1alpha1.Reservation, updated v1alpha1.Reservation) []v1alpha1.Reservation {
	result := make([]v1alpha1.Reservation, len(reservations))
	for i, res := range reservations {
		if res.Name == updated.Name {
			result[i] = updated
		} else {
			result[i] = res
		}
	}
	return result
}

// checkAllExistingVMsRemainEligible checks that after adding newVM to a reservation,
// all existing VMs in that reservation remain eligible.
// Returns (allEligible, failedVMUUID, reason).
func checkAllExistingVMsRemainEligible(
	newVM VM,
	reservation v1alpha1.Reservation,
	allReservations []v1alpha1.Reservation,
) (allEligible bool, failedVMUUID, reason string) {
	// Get existing allocations
	existingAllocations := reservation.Status.FailoverReservation.Allocations
	if existingAllocations == nil {
		return true, "", "" // No existing VMs to check
	}

	// Simulate adding newVM to the reservation
	updatedRes := reservation.DeepCopy()
	if updatedRes.Status.FailoverReservation == nil {
		updatedRes.Status.FailoverReservation = &v1alpha1.FailoverReservationStatus{}
	}
	if updatedRes.Status.FailoverReservation.Allocations == nil {
		updatedRes.Status.FailoverReservation.Allocations = make(map[string]string)
	}
	updatedRes.Status.FailoverReservation.Allocations[newVM.UUID] = newVM.CurrentHypervisor

	// Update allReservations with the modified reservation
	updatedAllRes := updateReservationInList(allReservations, *updatedRes)

	// Check each existing VM in the reservation
	for vmUUID, vmHost := range existingAllocations {
		existingVM := VM{UUID: vmUUID, CurrentHypervisor: vmHost, Resources: defaultVMResources}

		// Temporarily remove the VM to check if it can be "re-added"
		// This mimics what reconcileRemoveNoneligibleVMFromReservations does
		tempRes := updatedRes.DeepCopy()
		delete(tempRes.Status.FailoverReservation.Allocations, vmUUID)
		tempAllRes := updateReservationInList(updatedAllRes, *tempRes)

		if !IsVMEligibleForReservation(existingVM, *tempRes, tempAllRes) {
			return false, vmUUID, "VM became ineligible after adding " + newVM.UUID
		}
	}
	return true, "", ""
}

// TestAddingVMDoesNotMakeOthersIneligible tests that when a VM is eligible to be added
// to a reservation, adding it does not make existing VMs in that reservation ineligible.
// This is a critical invariant - if violated, the system will oscillate between adding
// and removing VMs from reservations.
func TestAddingVMDoesNotMakeOthersIneligible(t *testing.T) {
	testCases := []struct {
		name                    string
		vm                      VM
		reservation             v1alpha1.Reservation
		allReservations         []v1alpha1.Reservation
		vmIsEligible            bool   // Expected result of IsVMEligibleForReservation
		existingVMsStayEligible bool   // Expected: all existing VMs should stay eligible
		failingVM               string // If existingVMsStayEligible is false, which VM fails
	}{
		// ============================================================================
		// Cases where VM is eligible and existing VMs should stay eligible
		// ============================================================================
		{
			name:        "simple: add VM to empty reservation",
			vm:          makeVM("vm-2", "host2"),
			reservation: makeReservation("res-1", "host3", map[string]string{}),
			allReservations: []v1alpha1.Reservation{
				makeReservation("res-1", "host3", map[string]string{}),
			},
			vmIsEligible:            true,
			existingVMsStayEligible: true,
		},
		{
			name:        "simple: add VM to reservation with one VM on different host",
			vm:          makeVM("vm-2", "host2"),
			reservation: makeReservation("res-1", "host3", map[string]string{"vm-1": "host1"}),
			allReservations: []v1alpha1.Reservation{
				makeReservation("res-1", "host3", map[string]string{"vm-1": "host1"}),
			},
			vmIsEligible:            true,
			existingVMsStayEligible: true,
		},
		// ============================================================================
		// Cases where VM would make existing VMs ineligible if added
		// ============================================================================
		{
			// Scenario: vm-3 is eligible to join res-A, but vm-3 also uses res-B.
			// vm-1 already uses res-A. If vm-3 joins res-A, then vm-1's slots include res-A.
			// vm-3 uses res-A (one of vm-1's slots) and res-B.
			// Actually, vm-3 already uses res-B which vm-1 also uses, so vm-3 would use
			// two of vm-1's slots (res-A and res-B) -> constraint 5 violation!
			name: "ineligible: vm-3 already shares res-B with vm-1, cannot join res-A (constraint 5)",
			vm:   makeVM("vm-3", "host3"),
			reservation: makeReservation("res-A", "host4", map[string]string{
				"vm-1": "host1",
			}),
			allReservations: []v1alpha1.Reservation{
				makeReservation("res-A", "host4", map[string]string{"vm-1": "host1"}),
				makeReservation("res-B", "host5", map[string]string{"vm-1": "host1", "vm-3": "host3"}),
			},
			vmIsEligible:            false, // vm-3 would use two of vm-1's slots (res-A and res-B)
			existingVMsStayEligible: true,  // N/A since vm-3 can't join
		},
		{
			// Scenario with n=2: Each VM needs 2 reservations
			// vm-1 on host1 has res-A (host3) and res-B (host4)
			// vm-2 on host2 has res-A (host3) and res-C (host5)
			// vm-3 on host2 wants to join res-B
			// After vm-3 joins res-B:
			// - vm-1's slots: res-A, res-B
			// - VMs using vm-1's slots (other than vm-1): vm-2 (uses res-A), vm-3 (uses res-B)
			// - vm-2 is on host2, vm-3 is on host2 -> SAME HOST!
			// - Constraint 5 violated for vm-1!
			name: "ineligible: vm-3 on same host as vm-2 cannot join res-B (constraint 5)",
			vm:   makeVM("vm-3", "host2"), // Same host as vm-2!
			reservation: makeReservation("res-B", "host4", map[string]string{
				"vm-1": "host1",
			}),
			allReservations: []v1alpha1.Reservation{
				makeReservation("res-A", "host3", map[string]string{"vm-1": "host1", "vm-2": "host2"}),
				makeReservation("res-B", "host4", map[string]string{"vm-1": "host1"}),
				makeReservation("res-C", "host5", map[string]string{"vm-2": "host2"}),
			},
			vmIsEligible:            false, // EXPECTED: vm-3 should NOT be eligible (would break vm-1)
			existingVMsStayEligible: true,  // If vm-3 can't join, existing VMs stay eligible
		},
		{
			// Another scenario: vm-3 joins res-A where vm-1 and vm-2 already are
			// vm-1 on host1, vm-2 on host2, vm-3 on host3
			// res-A on host4 has vm-1 and vm-2
			// vm-3 wants to join res-A
			// vm-3 also has res-B on host5
			// After vm-3 joins:
			// - For vm-1: vm-1's slots include res-A
			// - VMs using res-A (other than vm-1): vm-2, vm-3
			// - vm-2 on host2, vm-3 on host3 -> different hosts, OK
			name: "OK: vm-3 joins res-A with vm-1 and vm-2, all on different hosts",
			vm:   makeVM("vm-3", "host3"),
			reservation: makeReservation("res-A", "host4", map[string]string{
				"vm-1": "host1",
				"vm-2": "host2",
			}),
			allReservations: []v1alpha1.Reservation{
				makeReservation("res-A", "host4", map[string]string{"vm-1": "host1", "vm-2": "host2"}),
				makeReservation("res-B", "host5", map[string]string{"vm-3": "host3"}),
			},
			vmIsEligible:            true,
			existingVMsStayEligible: true,
		},
		{
			// Constraint 1 violation scenario:
			// vm-1 on host1 has res-A (host3)
			// vm-2 on host3 (same as res-A's host!) wants to join res-A
			// Constraint 1 says VM cannot reserve on its own host
			// vm-2 is on host3, res-A is on host3 -> vm-2 is NOT eligible!
			name: "ineligible: vm-2 on reservation host cannot join res-A (constraint 1)",
			vm:   makeVM("vm-2", "host3"), // Same as res-A's host!
			reservation: makeReservation("res-A", "host3", map[string]string{
				"vm-1": "host1",
			}),
			allReservations: []v1alpha1.Reservation{
				makeReservation("res-A", "host3", map[string]string{"vm-1": "host1"}),
			},
			// Constraint 1: VM cannot reserve on its own host
			// vm-2 is on host3, res-A is on host3 -> vm-2 is NOT eligible
			vmIsEligible:            false, // Constraint 1 catches this
			existingVMsStayEligible: true,  // N/A since vm-2 can't join
		},
		{
			// Constraint 4 violation scenario:
			// vm-1 on host1 has res-A (host3) and res-B (host4)
			// vm-2 on host4 (same as res-B's host!) wants to join res-A
			// After adding vm-2 to res-A:
			// - For vm-1: vm-1's slots are res-A (host3) and res-B (host4)
			// - VMs using vm-1's slots (other than vm-1): vm-2 uses res-A
			// - Constraint 4: vm-2 must not run on vm-1's host (host1) or vm-1's slot hosts (host3, host4)
			// - vm-2 is on host4, which is vm-1's slot host -> VIOLATION!
			name: "ineligible: vm-2 on vm-1's slot host cannot join res-A (constraint 4)",
			vm:   makeVM("vm-2", "host4"), // Same as vm-1's res-B host!
			reservation: makeReservation("res-A", "host3", map[string]string{
				"vm-1": "host1",
			}),
			allReservations: []v1alpha1.Reservation{
				makeReservation("res-A", "host3", map[string]string{"vm-1": "host1"}),
				makeReservation("res-B", "host4", map[string]string{"vm-1": "host1"}),
			},
			vmIsEligible:            false, // EXPECTED: vm-2 should NOT be eligible (would break vm-1)
			existingVMsStayEligible: true,  // If vm-2 can't join, existing VMs stay eligible
		},
		// ============================================================================
		// Edge case: VM in OTHER reservations (not candidate) becomes ineligible
		// Must check VMs in allFailoverReservations, not just candidate reservation
		// ============================================================================
		{
			// Scenario:
			// vm-1 on host1 has res-A (host3) and res-B (host4)
			// vm-2 on host2 has res-B (host4) - shares res-B with vm-1
			// vm-3 on host2 wants to join res-A (which only has vm-1)
			//
			// After vm-3 joins res-A:
			// - For vm-1: vm-1's slots are res-A and res-B
			// - VMs using vm-1's slots: vm-3 (uses res-A), vm-2 (uses res-B)
			// - vm-3 is on host2, vm-2 is on host2 -> SAME HOST!
			// - Constraint 5 violated for vm-1!
			//
			// Must check vm-1 (in candidate res-A), but vm-2 is NOT in res-A.
			// vm-2 is in res-B, which is in allFailoverReservations.
			name: "ineligible: vm-3 on same host as vm-2 who shares vm-1's slot (constraint 5)",
			vm:   makeVM("vm-3", "host2"), // Same host as vm-2!
			reservation: makeReservation("res-A", "host3", map[string]string{
				"vm-1": "host1",
			}),
			allReservations: []v1alpha1.Reservation{
				makeReservation("res-A", "host3", map[string]string{"vm-1": "host1"}),
				makeReservation("res-B", "host4", map[string]string{"vm-1": "host1", "vm-2": "host2"}),
			},
			vmIsEligible:            false, // EXPECTED: vm-3 should NOT be eligible (would break vm-1)
			existingVMsStayEligible: true,  // If vm-3 can't join, existing VMs stay eligible
		},
		// ============================================================================
		// Complex scenarios with n=3 (3 reservations per VM)
		// ============================================================================
		{
			// Scenario with n=3:
			// vm-1 on host1 has res-A (host4), res-B (host5), res-C (host6)
			// vm-2 on host2 has res-A (host4), res-D (host7), res-E (host8)
			// vm-3 on host3 wants to join res-B (which only has vm-1)
			//
			// After vm-3 joins res-B:
			// - For vm-1: vm-1's slots are res-A, res-B, res-C
			// - VMs using vm-1's slots: vm-2 (uses res-A), vm-3 (uses res-B)
			// - vm-2 on host2, vm-3 on host3 -> different hosts, OK
			name: "n=3: OK - vm-3 joins res-B, vm-2 uses res-A, different hosts",
			vm:   makeVM("vm-3", "host3"),
			reservation: makeReservation("res-B", "host5", map[string]string{
				"vm-1": "host1",
			}),
			allReservations: []v1alpha1.Reservation{
				makeReservation("res-A", "host4", map[string]string{"vm-1": "host1", "vm-2": "host2"}),
				makeReservation("res-B", "host5", map[string]string{"vm-1": "host1"}),
				makeReservation("res-C", "host6", map[string]string{"vm-1": "host1"}),
				makeReservation("res-D", "host7", map[string]string{"vm-2": "host2"}),
				makeReservation("res-E", "host8", map[string]string{"vm-2": "host2"}),
			},
			vmIsEligible:            true,
			existingVMsStayEligible: true,
		},
		{
			// Scenario with n=3 - constraint 5 violation:
			// vm-1 on host1 has res-A (host4), res-B (host5), res-C (host6)
			// vm-2 on host2 has res-A (host4)
			// vm-3 on host2 wants to join res-B (which only has vm-1)
			//
			// After vm-3 joins res-B:
			// - For vm-1: vm-1's slots are res-A, res-B, res-C
			// - VMs using vm-1's slots: vm-2 (uses res-A), vm-3 (uses res-B)
			// - vm-2 on host2, vm-3 on host2 -> SAME HOST!
			// - Constraint 5 violated for vm-1!
			name: "n=3: ineligible - vm-3 on same host as vm-2 who uses vm-1's slot (constraint 5)",
			vm:   makeVM("vm-3", "host2"), // Same host as vm-2!
			reservation: makeReservation("res-B", "host5", map[string]string{
				"vm-1": "host1",
			}),
			allReservations: []v1alpha1.Reservation{
				makeReservation("res-A", "host4", map[string]string{"vm-1": "host1", "vm-2": "host2"}),
				makeReservation("res-B", "host5", map[string]string{"vm-1": "host1"}),
				makeReservation("res-C", "host6", map[string]string{"vm-1": "host1"}),
			},
			vmIsEligible:            false, // EXPECTED: vm-3 should NOT be eligible (would break vm-1)
			existingVMsStayEligible: true,
		},
		{
			// Scenario with n=3 - constraint 4 violation:
			// vm-1 on host1 has res-A (host4), res-B (host5), res-C (host6)
			// vm-2 on host5 (same as res-B!) wants to join res-A
			//
			// After vm-2 joins res-A:
			// - For vm-1: vm-1's slots are res-A, res-B, res-C
			// - VMs using vm-1's slots: vm-2 (uses res-A)
			// - Constraint 4: vm-2 must not run on vm-1's slot hosts (host4, host5, host6)
			// - vm-2 is on host5, which is vm-1's slot host -> VIOLATION!
			name: "n=3: ineligible - vm-2 on vm-1's slot host cannot join res-A (constraint 4)",
			vm:   makeVM("vm-2", "host5"), // Same as vm-1's res-B host!
			reservation: makeReservation("res-A", "host4", map[string]string{
				"vm-1": "host1",
			}),
			allReservations: []v1alpha1.Reservation{
				makeReservation("res-A", "host4", map[string]string{"vm-1": "host1"}),
				makeReservation("res-B", "host5", map[string]string{"vm-1": "host1"}),
				makeReservation("res-C", "host6", map[string]string{"vm-1": "host1"}),
			},
			vmIsEligible:            false, // EXPECTED: vm-2 should NOT be eligible (would break vm-1)
			existingVMsStayEligible: true,
		},
		// ============================================================================
		// Edge case: VM NOT in candidate reservation is affected
		// This tests if the fix correctly handles VMs that share slots with VMs in the candidate
		// ============================================================================
		{
			// Scenario:
			// vm-1 on host1 has res-A (host4) and res-B (host5)
			// vm-2 on host2 has res-A (host4) and res-C (host6)
			// vm-3 on host1 (same as vm-1!) wants to join res-C (which only has vm-2)
			//
			// After vm-3 joins res-C:
			// - For vm-2 (in res-C): vm-2's slots are res-A and res-C
			// - VMs using vm-2's slots: vm-1 (uses res-A), vm-3 (uses res-C)
			// - vm-1 on host1, vm-3 on host1 -> SAME HOST!
			// - Constraint 5 violated for vm-2!
			//
			// This is caught because vm-2 is in res-C (the candidate reservation).
			name: "edge: vm-3 joins res-C, makes vm-2 ineligible (vm-1 and vm-3 same host)",
			vm:   makeVM("vm-3", "host1"), // Same host as vm-1!
			reservation: makeReservation("res-C", "host6", map[string]string{
				"vm-2": "host2",
			}),
			allReservations: []v1alpha1.Reservation{
				makeReservation("res-A", "host4", map[string]string{"vm-1": "host1", "vm-2": "host2"}),
				makeReservation("res-B", "host5", map[string]string{"vm-1": "host1"}),
				makeReservation("res-C", "host6", map[string]string{"vm-2": "host2"}),
			},
			vmIsEligible:            false, // EXPECTED: vm-3 should NOT be eligible (would break vm-2)
			existingVMsStayEligible: true,
		},
		{
			// Scenario: 4 VMs, complex sharing
			// vm-1 on host1 has res-A (host5), res-B (host6)
			// vm-2 on host2 has res-A (host5), res-C (host7)
			// vm-3 on host3 has res-B (host6), res-C (host7)
			// vm-4 on host4 wants to join res-A
			//
			// After vm-4 joins res-A:
			// - For vm-1 (in res-A): vm-1's slots are res-A, res-B
			// - VMs using vm-1's slots: vm-2 (uses res-A), vm-3 (uses res-B), vm-4 (uses res-A)
			// - vm-2 on host2, vm-3 on host3, vm-4 on host4 -> all different hosts, OK
			// - For vm-2 (in res-A): vm-2's slots are res-A, res-C
			// - VMs using vm-2's slots: vm-1 (uses res-A), vm-3 (uses res-C), vm-4 (uses res-A)
			// - vm-1 on host1, vm-3 on host3, vm-4 on host4 -> all different hosts, OK
			name: "complex: 4 VMs, vm-4 joins res-A, all different hosts - OK",
			vm:   makeVM("vm-4", "host4"),
			reservation: makeReservation("res-A", "host5", map[string]string{
				"vm-1": "host1",
				"vm-2": "host2",
			}),
			allReservations: []v1alpha1.Reservation{
				makeReservation("res-A", "host5", map[string]string{"vm-1": "host1", "vm-2": "host2"}),
				makeReservation("res-B", "host6", map[string]string{"vm-1": "host1", "vm-3": "host3"}),
				makeReservation("res-C", "host7", map[string]string{"vm-2": "host2", "vm-3": "host3"}),
			},
			vmIsEligible:            true,
			existingVMsStayEligible: true,
		},
		{
			// Scenario: 4 VMs, complex sharing - constraint 5 violation
			// vm-1 on host1 has res-A (host5), res-B (host6)
			// vm-2 on host2 has res-A (host5), res-C (host7)
			// vm-3 on host3 has res-B (host6), res-C (host7)
			// vm-4 on host3 (same as vm-3!) wants to join res-A
			//
			// After vm-4 joins res-A:
			// - For vm-1 (in res-A): vm-1's slots are res-A, res-B
			// - VMs using vm-1's slots: vm-2 (uses res-A), vm-3 (uses res-B), vm-4 (uses res-A)
			// - vm-3 on host3, vm-4 on host3 -> SAME HOST!
			// - Constraint 5 violated for vm-1!
			name: "complex: ineligible - vm-4 on same host as vm-3 who uses vm-1's slot (constraint 5)",
			vm:   makeVM("vm-4", "host3"), // Same host as vm-3!
			reservation: makeReservation("res-A", "host5", map[string]string{
				"vm-1": "host1",
				"vm-2": "host2",
			}),
			allReservations: []v1alpha1.Reservation{
				makeReservation("res-A", "host5", map[string]string{"vm-1": "host1", "vm-2": "host2"}),
				makeReservation("res-B", "host6", map[string]string{"vm-1": "host1", "vm-3": "host3"}),
				makeReservation("res-C", "host7", map[string]string{"vm-2": "host2", "vm-3": "host3"}),
			},
			vmIsEligible:            false, // EXPECTED: vm-4 should NOT be eligible (would break vm-1)
			existingVMsStayEligible: true,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			// First, verify the VM's eligibility matches expectation
			isEligible := IsVMEligibleForReservation(tc.vm, tc.reservation, tc.allReservations)
			if isEligible != tc.vmIsEligible {
				t.Errorf("IsVMEligibleForReservation() = %v, expected %v", isEligible, tc.vmIsEligible)
				return
			}

			// If VM is not eligible, skip the "existing VMs stay eligible" check
			if !isEligible {
				return
			}

			// Check that all existing VMs remain eligible after adding the new VM
			allStayEligible, failedVM, reason := checkAllExistingVMsRemainEligible(
				tc.vm, tc.reservation, tc.allReservations,
			)

			if allStayEligible != tc.existingVMsStayEligible {
				if tc.existingVMsStayEligible {
					t.Errorf("Expected all existing VMs to stay eligible, but %s failed: %s", failedVM, reason)
				} else {
					t.Errorf("Expected VM %s to become ineligible, but all VMs stayed eligible", tc.failingVM)
				}
			}

			if !allStayEligible && tc.failingVM != "" && failedVM != tc.failingVM {
				t.Errorf("Expected VM %s to become ineligible, but VM %s failed instead", tc.failingVM, failedVM)
			}
		})
	}
}

// TestSymmetryOfEligibility tests that eligibility constraints are symmetric.
// If vm-A can share a reservation with vm-B, then vm-B should be able to share with vm-A
// (assuming they have equivalent reservation setups).
func TestSymmetryOfEligibility(t *testing.T) {
	testCases := []struct {
		name string
		vm1  VM
		vm2  VM
		// vm1Reservation is the reservation to check for vm1's eligibility
		vm1Reservation v1alpha1.Reservation
		// vm2Reservation is the reservation to check for vm2's eligibility
		vm2Reservation v1alpha1.Reservation
		// allReservationsForVM1 is the context when checking vm1's eligibility
		allReservationsForVM1 []v1alpha1.Reservation
		// allReservationsForVM2 is the context when checking vm2's eligibility
		allReservationsForVM2 []v1alpha1.Reservation
		vm1Eligible           bool
		vm2Eligible           bool
	}{
		{
			name:           "symmetric: both VMs can join empty reservation",
			vm1:            makeVM("vm-1", "host1"),
			vm2:            makeVM("vm-2", "host2"),
			vm1Reservation: makeReservation("res-1", "host3", map[string]string{}),
			vm2Reservation: makeReservation("res-1", "host3", map[string]string{}),
			allReservationsForVM1: []v1alpha1.Reservation{
				makeReservation("res-1", "host3", map[string]string{}),
			},
			allReservationsForVM2: []v1alpha1.Reservation{
				makeReservation("res-1", "host3", map[string]string{}),
			},
			vm1Eligible: true,
			vm2Eligible: true,
		},
		{
			name: "symmetric: vm-1 in res, vm-2 can join; vm-2 in res, vm-1 can join",
			vm1:  makeVM("vm-1", "host1"),
			vm2:  makeVM("vm-2", "host2"),
			// Check if vm-1 can join res-1 when vm-2 is already in it
			vm1Reservation: makeReservation("res-1", "host3", map[string]string{"vm-2": "host2"}),
			// Check if vm-2 can join res-1 when vm-1 is already in it
			vm2Reservation: makeReservation("res-1", "host3", map[string]string{"vm-1": "host1"}),
			allReservationsForVM1: []v1alpha1.Reservation{
				makeReservation("res-1", "host3", map[string]string{"vm-2": "host2"}),
			},
			allReservationsForVM2: []v1alpha1.Reservation{
				makeReservation("res-1", "host3", map[string]string{"vm-1": "host1"}),
			},
			vm1Eligible: true,
			vm2Eligible: true,
		},
		{
			name: "asymmetric: vm-1 has res on host2, vm-2 cannot join res on host3 with vm-1",
			vm1:  makeVM("vm-1", "host1"),
			vm2:  makeVM("vm-2", "host2"),
			// vm-1 is already in res-2, so we check if vm-1 can join a different reservation
			// For this test, vm-1 is already in res-2, so vm1Eligible is about whether vm-1 could join res-2 (it can't, it's already in it)
			vm1Reservation: makeReservation("res-2", "host3", map[string]string{"vm-1": "host1"}),
			vm2Reservation: makeReservation("res-2", "host3", map[string]string{"vm-1": "host1"}),
			allReservationsForVM1: []v1alpha1.Reservation{
				makeReservation("res-1", "host2", map[string]string{"vm-1": "host1"}),
				makeReservation("res-2", "host3", map[string]string{"vm-1": "host1"}),
			},
			allReservationsForVM2: []v1alpha1.Reservation{
				makeReservation("res-1", "host2", map[string]string{"vm-1": "host1"}),
				makeReservation("res-2", "host3", map[string]string{"vm-1": "host1"}),
			},
			// vm-1 is already in res-2, so it's not eligible to join again
			vm1Eligible: false,
			// vm-2 wants to join res-2 which has vm-1
			// has res-1 on host2, vm-2 is on host2
			// Constraint 4: vm-2 runs on vm-1's slot host (host2) -> VIOLATION
			vm2Eligible: false,
		},
		{
			name: "symmetric: both VMs on different hosts can share reservation",
			vm1:  makeVM("vm-1", "host1"),
			vm2:  makeVM("vm-2", "host2"),
			// Check if vm-1 can join res-1 when vm-2 is already in it
			vm1Reservation: makeReservation("res-1", "host3", map[string]string{"vm-2": "host2"}),
			// Check if vm-2 can join res-1 when vm-1 is already in it
			vm2Reservation: makeReservation("res-1", "host3", map[string]string{"vm-1": "host1"}),
			allReservationsForVM1: []v1alpha1.Reservation{
				makeReservation("res-1", "host3", map[string]string{"vm-2": "host2"}),
			},
			allReservationsForVM2: []v1alpha1.Reservation{
				makeReservation("res-1", "host3", map[string]string{"vm-1": "host1"}),
			},
			vm1Eligible: true,
			vm2Eligible: true,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			// Check vm1's eligibility to join the reservation
			vm1Result := IsVMEligibleForReservation(tc.vm1, tc.vm1Reservation, tc.allReservationsForVM1)
			if vm1Result != tc.vm1Eligible {
				t.Errorf("vm1 eligibility: got %v, expected %v", vm1Result, tc.vm1Eligible)
			}

			// Check vm2's eligibility to join the reservation
			vm2Result := IsVMEligibleForReservation(tc.vm2, tc.vm2Reservation, tc.allReservationsForVM2)
			if vm2Result != tc.vm2Eligible {
				t.Errorf("vm2 eligibility: got %v, expected %v", vm2Result, tc.vm2Eligible)
			}
		})
	}
}

// TestDataStructureConsistency tests that the internal data structures
// produce consistent results. This test will help verify the refactoring.
func TestDataStructureConsistency(t *testing.T) {
	// This test verifies that the helper functions produce consistent results
	// with the main IsVMEligibleForReservation function.

	testCases := []struct {
		name            string
		vm              VM
		reservation     v1alpha1.Reservation
		allReservations []v1alpha1.Reservation
	}{
		{
			name:        "simple case",
			vm:          makeVM("vm-1", "host1"),
			reservation: makeReservation("res-1", "host2", map[string]string{}),
			allReservations: []v1alpha1.Reservation{
				makeReservation("res-1", "host2", map[string]string{}),
			},
		},
		{
			name:        "complex case with multiple VMs and reservations",
			vm:          makeVM("vm-3", "host3"),
			reservation: makeReservation("res-A", "host4", map[string]string{"vm-1": "host1", "vm-2": "host2"}),
			allReservations: []v1alpha1.Reservation{
				makeReservation("res-A", "host4", map[string]string{"vm-1": "host1", "vm-2": "host2"}),
				makeReservation("res-B", "host5", map[string]string{"vm-1": "host1"}),
				makeReservation("res-C", "host6", map[string]string{"vm-2": "host2"}),
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			// Get the result from the main function
			result := IsVMEligibleForReservation(tc.vm, tc.reservation, tc.allReservations)

			// Verify helper functions produce consistent data
			vmHypervisorsMap := buildVMHypervisorsMap(tc.vm, tc.reservation, tc.allReservations)

			// Verify the VM is in the map
			if _, exists := vmHypervisorsMap[tc.vm.UUID]; !exists {
				t.Errorf("VM %s not found in vmHypervisorsMap", tc.vm.UUID)
			}

			// Verify the VM's current hypervisor is in the map
			if !vmHypervisorsMap[tc.vm.UUID][tc.vm.CurrentHypervisor] {
				t.Errorf("VM %s's current hypervisor %s not in vmHypervisorsMap", tc.vm.UUID, tc.vm.CurrentHypervisor)
			}

			// Log the result for debugging
			t.Logf("VM %s eligibility for %s: %v", tc.vm.UUID, tc.reservation.Name, result)
		})
	}
}

// TestFindEligibleReservations tests the FindEligibleReservations function.
func TestFindEligibleReservations(t *testing.T) {
	testCases := []struct {
		name                 string
		vm                   VM
		failoverReservations []v1alpha1.Reservation
		vmHostMap            map[string]string
		expectedCount        int
		expectedHosts        []string
	}{
		{
			name:                 "none: no reservations available",
			vm:                   makeVM("vm-1", "host1"),
			failoverReservations: []v1alpha1.Reservation{},
			vmHostMap: map[string]string{
				"vm-1": "host1",
			},
			expectedCount: 0,
			expectedHosts: nil,
		},
		{
			name: "one: single eligible reservation",
			vm:   makeVM("vm-2", "host2"),
			failoverReservations: []v1alpha1.Reservation{
				makeReservation("res-1", "host3", map[string]string{"vm-1": "host1"}),
			},
			vmHostMap: map[string]string{
				"vm-1": "host1",
				"vm-2": "host2",
			},
			expectedCount: 1,
			expectedHosts: []string{"host3"},
		},
		{
			name: "multiple: two eligible reservations",
			vm:   makeVM("vm-3", "host3"),
			failoverReservations: []v1alpha1.Reservation{
				makeReservation("res-1", "host1", map[string]string{"vm-1": "host2"}),
				makeReservation("res-2", "host2", map[string]string{"vm-2": "host1"}),
			},
			vmHostMap: map[string]string{
				"vm-1": "host2",
				"vm-2": "host1",
				"vm-3": "host3",
			},
			expectedCount: 2,
			expectedHosts: []string{"host1", "host2"},
		},
		{
			name: "none: all reservations on VM's host",
			vm:   makeVM("vm-1", "host1"),
			failoverReservations: []v1alpha1.Reservation{
				makeReservation("res-1", "host1", map[string]string{}),
			},
			vmHostMap: map[string]string{
				"vm-1": "host1",
			},
			expectedCount: 0,
			expectedHosts: nil,
		},
		{
			name: "none: VM already uses the reservation",
			vm:   makeVM("vm-1", "host1"),
			failoverReservations: []v1alpha1.Reservation{
				makeReservation("res-1", "host2", map[string]string{"vm-1": "host1"}),
			},
			vmHostMap: map[string]string{
				"vm-1": "host1",
			},
			expectedCount: 0,
			expectedHosts: nil,
		},
		{
			name: "filtered: one eligible after filtering",
			vm:   makeVM("vm-1", "host1"),
			failoverReservations: []v1alpha1.Reservation{
				makeReservation("res-1", "host2", map[string]string{"vm-2": "host1"}),
				makeReservation("res-2", "host3", map[string]string{"vm-3": "host2"}),
			},
			vmHostMap: map[string]string{
				"vm-1": "host1",
				"vm-2": "host1",
				"vm-3": "host2",
			},
			expectedCount: 1,
			expectedHosts: []string{"host3"},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			// The new API builds VMHostsMap from the VM and failoverReservations
			// No need to add temp reservations - the VM's host is included automatically
			result := FindEligibleReservations(tc.vm, tc.failoverReservations)

			if len(result) != tc.expectedCount {
				t.Errorf("FindEligibleReservations() returned %d reservations, expected %d", len(result), tc.expectedCount)
			}

			if tc.expectedHosts != nil {
				resultHosts := make([]string, len(result))
				for i, res := range result {
					resultHosts[i] = res.Status.Host
				}

				for _, expectedHost := range tc.expectedHosts {
					found := false
					for _, resultHost := range resultHosts {
						if resultHost == expectedHost {
							found = true
							break
						}
					}
					if !found {
						t.Errorf("Expected host %s not found in results %v", expectedHost, resultHosts)
					}
				}
			}
		})
	}
}
