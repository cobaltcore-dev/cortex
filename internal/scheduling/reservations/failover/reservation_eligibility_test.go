// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package failover

import (
	"testing"

	"github.com/cobaltcore-dev/cortex/api/v1alpha1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// Default resources for test VMs and reservations (4GB memory, 2 vcpus)
var defaultResources = map[string]resource.Quantity{
	"memory": resource.MustParse("4Gi"),
	"vcpus":  resource.MustParse("2"),
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
func makeReservationWithResources(name, host string, usedBy map[string]string, resources map[string]resource.Quantity) v1alpha1.Reservation { //nolint:unparam // name is always "res-1" in tests but kept for clarity
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
		// ============================================================================
		// Constraint 2: VM's reservations must be on distinct hosts
		// ============================================================================
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
			name:        "integration: vm-3 should be eligible for reservation on host1",
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

// TestDoesVMFitInReservation tests the DoesVMFitInReservation function.
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
			reservation: makeReservationWithResources("res-1", "host2", map[string]string{}, map[string]resource.Quantity{
				"memory": resource.MustParse("4Gi"),
				"vcpus":  resource.MustParse("2"),
			}),
			expected: true,
		},
		{
			name: "fits: VM is smaller than reservation",
			vm: makeVMWithResources("vm-1", "host1", map[string]resource.Quantity{
				"memory": resource.MustParse("2Gi"),
				"vcpus":  resource.MustParse("1"),
			}),
			reservation: makeReservationWithResources("res-1", "host2", map[string]string{}, map[string]resource.Quantity{
				"memory": resource.MustParse("4Gi"),
				"vcpus":  resource.MustParse("2"),
			}),
			expected: true,
		},
		{
			name: "exceeds: VM memory exceeds reservation",
			vm: makeVMWithResources("vm-1", "host1", map[string]resource.Quantity{
				"memory": resource.MustParse("8Gi"),
				"vcpus":  resource.MustParse("2"),
			}),
			reservation: makeReservationWithResources("res-1", "host2", map[string]string{}, map[string]resource.Quantity{
				"memory": resource.MustParse("4Gi"),
				"vcpus":  resource.MustParse("2"),
			}),
			expected: false,
		},
		{
			name: "exceeds: VM vcpus exceeds reservation",
			vm: makeVMWithResources("vm-1", "host1", map[string]resource.Quantity{
				"memory": resource.MustParse("4Gi"),
				"vcpus":  resource.MustParse("4"),
			}),
			reservation: makeReservationWithResources("res-1", "host2", map[string]string{}, map[string]resource.Quantity{
				"memory": resource.MustParse("4Gi"),
				"vcpus":  resource.MustParse("2"),
			}),
			expected: false,
		},
		{
			name: "fits: VM has no resources defined",
			vm:   makeVMWithResources("vm-1", "host1", map[string]resource.Quantity{}),
			reservation: makeReservationWithResources("res-1", "host2", map[string]string{}, map[string]resource.Quantity{
				"memory": resource.MustParse("4Gi"),
				"vcpus":  resource.MustParse("2"),
			}),
			expected: true,
		},
		{
			name: "exceeds: reservation has no memory resource",
			vm: makeVMWithResources("vm-1", "host1", map[string]resource.Quantity{
				"memory": resource.MustParse("4Gi"),
				"vcpus":  resource.MustParse("2"),
			}),
			reservation: makeReservationWithResources("res-1", "host2", map[string]string{}, map[string]resource.Quantity{
				"vcpus": resource.MustParse("2"),
			}),
			expected: false,
		},
		{
			name: "exceeds: reservation has no vcpus resource",
			vm: makeVMWithResources("vm-1", "host1", map[string]resource.Quantity{
				"memory": resource.MustParse("4Gi"),
				"vcpus":  resource.MustParse("2"),
			}),
			reservation: makeReservationWithResources("res-1", "host2", map[string]string{}, map[string]resource.Quantity{
				"memory": resource.MustParse("4Gi"),
			}),
			expected: false,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			result := DoesVMFitInReservation(tc.vm, tc.reservation)

			if result != tc.expected {
				t.Errorf("DoesVMFitInReservation() = %v, expected %v", result, tc.expected)
			}
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
