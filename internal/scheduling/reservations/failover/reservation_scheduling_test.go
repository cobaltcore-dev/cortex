// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package failover

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	novaapi "github.com/cobaltcore-dev/cortex/api/external/nova"
	"github.com/cobaltcore-dev/cortex/api/v1alpha1"
	"github.com/cobaltcore-dev/cortex/internal/knowledge/extractor/plugins/compute"
	"github.com/cobaltcore-dev/cortex/internal/scheduling/reservations"
	hv1 "github.com/cobaltcore-dev/openstack-hypervisor-operator/api/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

// ============================================================================
// Test: buildReservationWithVM
// ============================================================================

func TestBuildReservationWithVM(t *testing.T) {
	tests := []struct {
		name                   string
		reservation            v1alpha1.Reservation
		vm                     reservations.VM
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

			result := addVMToReservation(tt.reservation, tt.vm)

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
		vm             reservations.VM
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
			ctx := context.Background()
			creator := "test-creator"

			// Resolve using VM's own resources (no flavor groups)
			resolved := resolveVMSpecForScheduling(ctx, tt.vm, false, nil)
			result := newFailoverReservation(ctx, tt.vm, tt.hypervisor, creator, resolved)

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

			// Verify resources are derived from VM
			// Note: VM uses "vcpus" but reservation uses "cpu" as the canonical key.
			// Memory uses binary MiB (bytes / 1024*1024 → MiB → MiB * 1024*1024), matching
			// commitments/state.go and vm_source.go conventions.
			if tt.vm.Resources != nil {
				if memory, ok := tt.vm.Resources["memory"]; ok {
					if resMemory, ok := result.Spec.Resources[hv1.ResourceMemory]; !ok {
						t.Error("reservation missing memory resource")
					} else if !memory.Equal(resMemory) {
						t.Errorf("memory resource = %v, want %v", resMemory, memory)
					}
				}
				if vcpus, ok := tt.vm.Resources["vcpus"]; ok {
					// VM uses "vcpus" but reservation should use "cpu"
					if resCPU, ok := result.Spec.Resources[hv1.ResourceCPU]; !ok {
						t.Error("reservation missing cpu resource")
					} else if !vcpus.Equal(resCPU) {
						t.Errorf("cpu resource = %v, want %v", resCPU, vcpus)
					}
				}
			}

			// Verify labels
			if result.Labels["cortex.cloud/creator"] != "test-creator" {
				t.Errorf("creator label = %v, want %v", result.Labels["cortex.cloud/creator"], "test-creator")
			}
			if result.Labels[v1alpha1.LabelReservationType] != v1alpha1.ReservationTypeLabelFailover {
				t.Errorf("type label = %v, want %v", result.Labels[v1alpha1.LabelReservationType], v1alpha1.ReservationTypeLabelFailover)
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
// Test: resolveVMSpecForScheduling + newFailoverReservation with flavor group resources
// ============================================================================

func TestResolveVMForSchedulingAndNewFailoverReservation(t *testing.T) {
	// Build a flavor group where the VM's flavor is "hana_c60_m960" (small)
	// but the LargestFlavor is "hana_c120_m1920" (large).
	// When UseFlavorGroupResources is true, the resolved resources should use
	// the LargestFlavor's name and size. The reservation should then be sized accordingly.
	flavorGroups := map[string]compute.FlavorGroupFeature{
		"hana_v2": {
			Name: "hana_v2",
			Flavors: []compute.FlavorInGroup{
				{Name: "hana_c120_m1920", VCPUs: 120, MemoryMB: 1966080},
				{Name: "hana_c60_m960", VCPUs: 60, MemoryMB: 983040},
				{Name: "hana_c30_m480", VCPUs: 30, MemoryMB: 491520},
			},
			LargestFlavor:  compute.FlavorInGroup{Name: "hana_c120_m1920", VCPUs: 120, MemoryMB: 1966080},
			SmallestFlavor: compute.FlavorInGroup{Name: "hana_c30_m480", VCPUs: 30, MemoryMB: 491520},
		},
	}

	tests := []struct {
		name                    string
		vm                      reservations.VM
		useFlavorGroupResources bool
		flavorGroups            map[string]compute.FlavorGroupFeature
		wantFlavorName          string
		wantFlavorGroupName     string
		wantResourceGroup       string
		wantMemoryMB            uint64
		wantVCPUs               uint64
	}{
		{
			name: "uses LargestFlavor resources when enabled and flavor found",
			vm: reservations.VM{
				UUID:              "vm-1",
				CurrentHypervisor: "host1",
				FlavorName:        "hana_c60_m960",
				ProjectID:         "test-project",
				Resources: map[string]resource.Quantity{
					"vcpus":  *resource.NewQuantity(60, resource.DecimalSI),
					"memory": *resource.NewQuantity(983040*1024*1024, resource.BinarySI),
				},
			},
			useFlavorGroupResources: true,
			flavorGroups:            flavorGroups,
			wantFlavorName:          "hana_c120_m1920", // LargestFlavor name
			wantFlavorGroupName:     "hana_v2",         // flavor group name
			wantResourceGroup:       "hana_v2",         // ResourceGroup = flavor group name
			wantMemoryMB:            1966080,           // LargestFlavor memory
			wantVCPUs:               120,               // LargestFlavor vcpus
		},
		{
			name: "falls back to VM resources when disabled",
			vm: reservations.VM{
				UUID:              "vm-2",
				CurrentHypervisor: "host1",
				FlavorName:        "hana_c60_m960",
				ProjectID:         "test-project",
				Resources: map[string]resource.Quantity{
					"vcpus":  *resource.NewQuantity(60, resource.DecimalSI),
					"memory": *resource.NewQuantity(983040*1024*1024, resource.BinarySI),
				},
			},
			useFlavorGroupResources: false,
			flavorGroups:            flavorGroups,
			wantFlavorName:          "hana_c60_m960", // VM's own flavor name
			wantFlavorGroupName:     "",              // no flavor group (disabled)
			wantResourceGroup:       "hana_c60_m960", // ResourceGroup = fallback to flavor name
			wantMemoryMB:            983040,          // VM's own memory (MiB, binary)
			wantVCPUs:               60,              // VM's own vcpus
		},
		{
			name: "falls back to VM resources when flavor not in any group",
			vm: reservations.VM{
				UUID:              "vm-3",
				CurrentHypervisor: "host1",
				FlavorName:        "unknown_flavor",
				ProjectID:         "test-project",
				Resources: map[string]resource.Quantity{
					"vcpus":  *resource.NewQuantity(8, resource.DecimalSI),
					"memory": *resource.NewQuantity(16384*1024*1024, resource.BinarySI),
				},
			},
			useFlavorGroupResources: true,
			flavorGroups:            flavorGroups,
			wantFlavorName:          "unknown_flavor", // VM's own flavor name (fallback)
			wantFlavorGroupName:     "",               // no flavor group (not found)
			wantResourceGroup:       "unknown_flavor", // ResourceGroup = fallback to flavor name
			wantMemoryMB:            16384,            // VM's own memory (MiB, binary)
			wantVCPUs:               8,                // VM's own vcpus (fallback)
		},
		{
			name: "falls back to VM resources when flavorGroups is nil",
			vm: reservations.VM{
				UUID:              "vm-4",
				CurrentHypervisor: "host1",
				FlavorName:        "hana_c60_m960",
				ProjectID:         "test-project",
				Resources: map[string]resource.Quantity{
					"vcpus":  *resource.NewQuantity(60, resource.DecimalSI),
					"memory": *resource.NewQuantity(983040*1024*1024, resource.BinarySI),
				},
			},
			useFlavorGroupResources: true,
			flavorGroups:            nil,             // nil flavor groups
			wantFlavorName:          "hana_c60_m960", // VM's own flavor name (fallback)
			wantFlavorGroupName:     "",              // no flavor group (nil groups)
			wantResourceGroup:       "hana_c60_m960", // ResourceGroup = fallback to flavor name
			wantMemoryMB:            983040,          // VM's own memory (MiB, binary)
			wantVCPUs:               60,              // VM's own vcpus (fallback)
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := context.Background()
			creator := "test-creator"

			// Test resolveVMSpecForScheduling
			resolved := resolveVMSpecForScheduling(ctx, tt.vm, tt.useFlavorGroupResources, tt.flavorGroups)

			if resolved.FlavorName != tt.wantFlavorName {
				t.Errorf("resolved.FlavorName = %q, want %q", resolved.FlavorName, tt.wantFlavorName)
			}
			if resolved.MemoryMB != tt.wantMemoryMB {
				t.Errorf("resolved.MemoryMB = %d, want %d", resolved.MemoryMB, tt.wantMemoryMB)
			}
			if resolved.VCPUs != tt.wantVCPUs {
				t.Errorf("resolved.VCPUs = %d, want %d", resolved.VCPUs, tt.wantVCPUs)
			}
			if resolved.FlavorGroupName != tt.wantFlavorGroupName {
				t.Errorf("resolved.FlavorGroupName = %q, want %q", resolved.FlavorGroupName, tt.wantFlavorGroupName)
			}

			// Test that newFailoverReservation uses the resolved values correctly
			result := newFailoverReservation(ctx, tt.vm, "target-host", creator, resolved)

			// Verify reservation memory matches resolved
			resMemory, ok := result.Spec.Resources[hv1.ResourceMemory]
			if !ok {
				t.Fatal("reservation missing memory resource")
			}
			wantMemoryBytes := int64(tt.wantMemoryMB) * 1024 * 1024 //nolint:gosec // test values won't overflow; binary MiB matches OpenStack convention
			if resMemory.Value() != wantMemoryBytes {
				t.Errorf("reservation memory = %d bytes, want %d bytes", resMemory.Value(), wantMemoryBytes)
			}

			// Verify reservation CPU matches resolved
			resCPU, ok := result.Spec.Resources[hv1.ResourceCPU]
			if !ok {
				t.Fatal("reservation missing cpu resource")
			}
			if resCPU.Value() != int64(tt.wantVCPUs) { //nolint:gosec // test values won't overflow
				t.Errorf("reservation cpu = %d, want %d", resCPU.Value(), tt.wantVCPUs)
			}

			// Verify ResourceGroup on the reservation
			if result.Spec.FailoverReservation == nil {
				t.Fatal("reservation missing FailoverReservation spec")
			}
			if result.Spec.FailoverReservation.ResourceGroup != tt.wantResourceGroup {
				t.Errorf("ResourceGroup = %q, want %q", result.Spec.FailoverReservation.ResourceGroup, tt.wantResourceGroup)
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
			Resources: map[hv1.ResourceName]resource.Quantity{
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
			Resources: map[hv1.ResourceName]resource.Quantity{
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

func buildSchedulingTestVM(uuid, hypervisor string) reservations.VM { //nolint:unparam // uuid may vary in future tests
	return reservations.VM{
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

func buildSchedulingTestVMWithResources(uuid, hypervisor string, memoryMB, vcpus int64) reservations.VM {
	return reservations.VM{
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

// captureSchedulerRequest spins up a test HTTP server that captures one scheduler request
// and returns a single host. The captured request is written into *out.
func captureSchedulerRequest(t *testing.T, host string, out *novaapi.ExternalSchedulerRequest) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(out); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		resp := novaapi.ExternalSchedulerResponse{Hosts: []string{host}}
		_ = json.NewEncoder(w).Encode(resp) //nolint:errcheck
	}))
}

// buildMinimalController returns a FailoverReservationController wired to a scheduler at the given URL.
func buildMinimalController(t *testing.T, schedulerURL string) *FailoverReservationController {
	t.Helper()
	scheme := runtime.NewScheme()
	if err := v1alpha1.AddToScheme(scheme); err != nil {
		t.Fatalf("add v1alpha1 to scheme: %v", err)
	}
	if err := hv1.AddToScheme(scheme); err != nil {
		t.Fatalf("add hv1 to scheme: %v", err)
	}
	k8sClient := fake.NewClientBuilder().WithScheme(scheme).Build()
	schedulerClient := reservations.NewSchedulerClient(schedulerURL + "/scheduler/nova/external")
	config := FailoverConfig{}
	config.ApplyDefaults()
	return NewFailoverReservationController(k8sClient, nil, config, schedulerClient, nil)
}

// ============================================================================
// Test: scheduling options sent to the scheduler
// ============================================================================

func TestFailoverSchedulerOptions(t *testing.T) {
	vm := buildSchedulingTestVM("vm-1", "host-1")
	vm.AvailabilityZone = "az1"
	resolved := resolveVMSpecForScheduling(context.Background(), vm, false, nil)
	reservation := buildSchedulingTestReservation("res-1", "host-2", nil)
	reservation.Status.FailoverReservation = &v1alpha1.FailoverReservationStatus{Allocations: map[string]string{}}

	tests := []struct {
		name                 string
		call                 func(c *FailoverReservationController, ctx context.Context)
		wantReadOnly         bool
		wantLockReservations bool
	}{
		{
			// Read-only compatibility check — must not write scheduling state.
			// Tenant-context filters must run so only hosts the VM can actually reach are returned.
			name: "tryReuseExistingReservation",
			call: func(c *FailoverReservationController, ctx context.Context) {
				_ = c.tryReuseExistingReservation(ctx, vm, []v1alpha1.Reservation{reservation}, []string{"host-1", "host-2"}, resolved)
			},
			wantReadOnly:         true,
			wantLockReservations: false,
		},
		{
			// Validates whether a VM can land on the reservation host during evacuation.
			// LockReservations ensures failover capacity is not unlocked for other VMs.
			// Tenant-context filters (aggregate metadata, allowed projects, instance group) must run.
			name: "validateVMViaSchedulerEvacuation",
			call: func(c *FailoverReservationController, ctx context.Context) {
				_, _ = c.validateVMViaSchedulerEvacuation(ctx, vm, "host-2") //nolint:errcheck
			},
			wantReadOnly:         true,
			wantLockReservations: true,
		},
		{
			// Finds a host for a new reservation — writes state, so not read-only.
			// LockReservations ensures existing reservations are treated as unavailable.
			// Tenant-context filters must run.
			name: "scheduleAndBuildNewFailoverReservation",
			call: func(c *FailoverReservationController, ctx context.Context) {
				_, _ = c.scheduleAndBuildNewFailoverReservation(ctx, vm, []string{"host-1", "host-2"}, nil, nil, resolved) //nolint:errcheck
			},
			wantReadOnly:         false,
			wantLockReservations: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var capturedReq novaapi.ExternalSchedulerRequest
			server := captureSchedulerRequest(t, "host-2", &capturedReq)
			defer server.Close()

			tt.call(buildMinimalController(t, server.URL), context.Background())

			if capturedReq.Options.ReadOnly != tt.wantReadOnly {
				t.Errorf("ReadOnly = %v, want %v", capturedReq.Options.ReadOnly, tt.wantReadOnly)
			}
			if capturedReq.Options.LockReservations != tt.wantLockReservations {
				t.Errorf("LockReservations = %v, want %v", capturedReq.Options.LockReservations, tt.wantLockReservations)
			}
		})
	}
}
