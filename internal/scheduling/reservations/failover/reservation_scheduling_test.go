// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package failover

import (
	"testing"

	"github.com/cobaltcore-dev/cortex/api/v1alpha1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// ============================================================================
// Test: buildReservationWithVM
// ============================================================================

func TestBuildReservationWithVM(t *testing.T) {
	tests := []struct {
		name                   string
		reservation            v1alpha1.Reservation
		vm                     VM
		wantVMInAllocations    bool
		wantAllocationsCount   int
		wantOriginalUnmodified bool
	}{
		{
			name:                   "adds VM to empty reservation",
			reservation:            buildSchedulingTestReservation("res-1", "host2", nil),
			vm:                     buildSchedulingTestVM("vm-1", "host1"),
			wantVMInAllocations:    true,
			wantAllocationsCount:   1,
			wantOriginalUnmodified: true,
		},
		{
			name:                   "adds VM to reservation with existing allocations",
			reservation:            buildSchedulingTestReservation("res-1", "host2", map[string]string{"vm-2": "host3"}),
			vm:                     buildSchedulingTestVM("vm-1", "host1"),
			wantVMInAllocations:    true,
			wantAllocationsCount:   2,
			wantOriginalUnmodified: true,
		},
		{
			name:                   "adds VM to reservation with nil FailoverReservation status",
			reservation:            buildSchedulingTestReservationNoStatus("res-1", "host2"),
			vm:                     buildSchedulingTestVM("vm-1", "host1"),
			wantVMInAllocations:    true,
			wantAllocationsCount:   1,
			wantOriginalUnmodified: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Store original allocations count for verification
			originalAllocCount := 0
			if tt.reservation.Status.FailoverReservation != nil {
				originalAllocCount = len(tt.reservation.Status.FailoverReservation.Allocations)
			}

			result := buildReservationWithVM(tt.reservation, tt.vm)

			// Verify result has FailoverReservation status
			if result.Status.FailoverReservation == nil {
				t.Fatal("result has no FailoverReservation status")
			}

			// Verify VM is in allocations
			allocatedHost, exists := result.Status.FailoverReservation.Allocations[tt.vm.UUID]
			if exists != tt.wantVMInAllocations {
				t.Errorf("VM in allocations = %v, want %v", exists, tt.wantVMInAllocations)
			}

			// Verify allocated host matches VM's current hypervisor
			if exists && allocatedHost != tt.vm.CurrentHypervisor {
				t.Errorf("allocated host = %v, want %v", allocatedHost, tt.vm.CurrentHypervisor)
			}

			// Verify allocations count
			if len(result.Status.FailoverReservation.Allocations) != tt.wantAllocationsCount {
				t.Errorf("allocations count = %d, want %d",
					len(result.Status.FailoverReservation.Allocations), tt.wantAllocationsCount)
			}

			// Verify original reservation is not modified
			if tt.wantOriginalUnmodified {
				currentOriginalCount := 0
				if tt.reservation.Status.FailoverReservation != nil {
					currentOriginalCount = len(tt.reservation.Status.FailoverReservation.Allocations)
				}
				if currentOriginalCount != originalAllocCount {
					t.Errorf("original reservation was modified: allocations count changed from %d to %d",
						originalAllocCount, currentOriginalCount)
				}
			}
		})
	}
}

// ============================================================================
// Test: buildNewFailoverReservation
// ============================================================================

func TestBuildNewFailoverReservation(t *testing.T) {
	tests := []struct {
		name           string
		vm             VM
		hypervisor     string
		wantHost       string
		wantTargetHost string
		wantVMInAlloc  bool
		wantType       v1alpha1.ReservationType
	}{
		{
			name:           "creates reservation with correct host and VM",
			vm:             buildSchedulingTestVM("vm-1", "host1"),
			hypervisor:     "host2",
			wantHost:       "host2",
			wantTargetHost: "host2",
			wantVMInAlloc:  true,
			wantType:       v1alpha1.ReservationTypeFailover,
		},
		{
			name:           "creates reservation with VM resources",
			vm:             buildSchedulingTestVMWithResources("vm-2", "host3", 8192, 4),
			hypervisor:     "host4",
			wantHost:       "host4",
			wantTargetHost: "host4",
			wantVMInAlloc:  true,
			wantType:       v1alpha1.ReservationTypeFailover,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			controller := &FailoverReservationController{
				Config: FailoverConfig{Creator: "test-creator"},
			}

			result := controller.buildNewFailoverReservation(tt.vm, tt.hypervisor)

			// Verify Status.Host
			if result.Status.Host != tt.wantHost {
				t.Errorf("Status.Host = %v, want %v", result.Status.Host, tt.wantHost)
			}

			// Verify Spec.TargetHost
			if result.Spec.TargetHost != tt.wantTargetHost {
				t.Errorf("Spec.TargetHost = %v, want %v", result.Spec.TargetHost, tt.wantTargetHost)
			}

			// Verify Type
			if result.Spec.Type != tt.wantType {
				t.Errorf("Spec.Type = %v, want %v", result.Spec.Type, tt.wantType)
			}

			// Verify VM is in allocations
			if result.Status.FailoverReservation == nil {
				t.Fatal("result has no FailoverReservation status")
			}
			allocatedHost, exists := result.Status.FailoverReservation.Allocations[tt.vm.UUID]
			if exists != tt.wantVMInAlloc {
				t.Errorf("VM in allocations = %v, want %v", exists, tt.wantVMInAlloc)
			}
			if exists && allocatedHost != tt.vm.CurrentHypervisor {
				t.Errorf("allocated host = %v, want %v", allocatedHost, tt.vm.CurrentHypervisor)
			}

			// Verify resources are copied from VM
			if tt.vm.Resources != nil {
				if memory, ok := tt.vm.Resources["memory"]; ok {
					if resMemory, ok := result.Spec.Resources["memory"]; !ok {
						t.Error("reservation missing memory resource")
					} else if !memory.Equal(resMemory) {
						t.Errorf("memory resource = %v, want %v", resMemory, memory)
					}
				}
				if vcpus, ok := tt.vm.Resources["vcpus"]; ok {
					if resVCPUs, ok := result.Spec.Resources["vcpus"]; !ok {
						t.Error("reservation missing vcpus resource")
					} else if !vcpus.Equal(resVCPUs) {
						t.Errorf("vcpus resource = %v, want %v", resVCPUs, vcpus)
					}
				}
			}

			// Verify labels
			if result.Labels["cortex.sap.com/creator"] != "test-creator" {
				t.Errorf("creator label = %v, want %v", result.Labels["cortex.sap.com/creator"], "test-creator")
			}
			if result.Labels["cortex.sap.com/type"] != "failover" {
				t.Errorf("type label = %v, want %v", result.Labels["cortex.sap.com/type"], "failover")
			}

			// Verify GenerateName is set
			if result.GenerateName != "failover-" {
				t.Errorf("GenerateName = %v, want %v", result.GenerateName, "failover-")
			}

			// Verify Ready condition is set
			if len(result.Status.Conditions) == 0 {
				t.Error("no conditions set on reservation")
			} else {
				foundReady := false
				for _, cond := range result.Status.Conditions {
					if cond.Type == v1alpha1.ReservationConditionReady {
						foundReady = true
						if cond.Status != metav1.ConditionTrue {
							t.Errorf("Ready condition status = %v, want %v", cond.Status, metav1.ConditionTrue)
						}
					}
				}
				if !foundReady {
					t.Error("Ready condition not found")
				}
			}
		})
	}
}

// ============================================================================
// Test Helpers (local to this test file)
// ============================================================================

func buildSchedulingTestReservation(name, host string, allocations map[string]string) v1alpha1.Reservation {
	res := v1alpha1.Reservation{
		ObjectMeta: metav1.ObjectMeta{Name: name},
		Spec: v1alpha1.ReservationSpec{
			Type:       v1alpha1.ReservationTypeFailover,
			TargetHost: host,
			Resources: map[string]resource.Quantity{
				"memory": resource.MustParse("8Gi"),
				"vcpus":  resource.MustParse("4"),
			},
		},
		Status: v1alpha1.ReservationStatus{
			Host: host,
		},
	}
	if allocations != nil {
		res.Status.FailoverReservation = &v1alpha1.FailoverReservationStatus{
			Allocations: allocations,
		}
	}
	return res
}

func buildSchedulingTestReservationNoStatus(name, host string) v1alpha1.Reservation {
	return v1alpha1.Reservation{
		ObjectMeta: metav1.ObjectMeta{Name: name},
		Spec: v1alpha1.ReservationSpec{
			Type:       v1alpha1.ReservationTypeFailover,
			TargetHost: host,
			Resources: map[string]resource.Quantity{
				"memory": resource.MustParse("8Gi"),
				"vcpus":  resource.MustParse("4"),
			},
		},
		Status: v1alpha1.ReservationStatus{
			Host: host,
			// FailoverReservation is nil
		},
	}
}

func buildSchedulingTestVM(uuid, hypervisor string) VM { //nolint:unparam // uuid may vary in future tests
	return VM{
		UUID:              uuid,
		CurrentHypervisor: hypervisor,
		FlavorName:        "m1.large",
		ProjectID:         "test-project",
		Resources: map[string]resource.Quantity{
			"vcpus":  resource.MustParse("4"),
			"memory": resource.MustParse("8Gi"),
		},
	}
}

func buildSchedulingTestVMWithResources(uuid, hypervisor string, memoryMB, vcpus int64) VM {
	return VM{
		UUID:              uuid,
		CurrentHypervisor: hypervisor,
		FlavorName:        "m1.large",
		ProjectID:         "test-project",
		Resources: map[string]resource.Quantity{
			"vcpus":  *resource.NewQuantity(vcpus, resource.DecimalSI),
			"memory": *resource.NewQuantity(memoryMB*1024*1024, resource.BinarySI),
		},
	}
}
