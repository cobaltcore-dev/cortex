// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package commitments

import (
	"context"
	"testing"

	"github.com/cobaltcore-dev/cortex/api/v1alpha1"
	"github.com/cobaltcore-dev/cortex/internal/knowledge/extractor/plugins/compute"
	hv1 "github.com/cobaltcore-dev/openstack-hypervisor-operator/api/v1"
	"github.com/go-logr/logr"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// newTestCRSlot creates a Reservation slot for commitment "abc123" / project "project-1".
// Pass nil allocs for an empty allocation map.
func newTestCRSlot(name string, memGiB int64, targetHost, resourceGroup string, allocs map[string]v1alpha1.CommittedResourceAllocation) v1alpha1.Reservation {
	if allocs == nil {
		allocs = map[string]v1alpha1.CommittedResourceAllocation{}
	}
	return v1alpha1.Reservation{
		ObjectMeta: metav1.ObjectMeta{
			Name: name,
			Labels: map[string]string{
				v1alpha1.LabelReservationType: v1alpha1.ReservationTypeLabelCommittedResource,
			},
		},
		Spec: v1alpha1.ReservationSpec{
			TargetHost: targetHost,
			Resources: map[hv1.ResourceName]resource.Quantity{
				hv1.ResourceMemory: *resource.NewQuantity(memGiB*1024*1024*1024, resource.BinarySI),
			},
			CommittedResourceReservation: &v1alpha1.CommittedResourceReservationSpec{
				CommitmentUUID: "abc123",
				ProjectID:      "project-1",
				ResourceGroup:  resourceGroup,
				Creator:        "syncer",
				Allocations:    allocs,
			},
		},
	}
}

// testFlavorGroups returns the default flavor groups map used across tests.
func testFlavorGroups() map[string]compute.FlavorGroupFeature {
	return map[string]compute.FlavorGroupFeature{"test-group": testFlavorGroup()}
}

// ============================================================================
// Tests: ApplyCommitmentState
// ============================================================================

func TestApplyCommitmentState(t *testing.T) {
	tests := []struct {
		name                string
		existingSlots       []v1alpha1.Reservation
		desiredMemoryGiB    int64
		flavorGroupOverride map[string]compute.FlavorGroupFeature // nil = testFlavorGroups()
		wantError           bool
		wantRemovedCount    int // exact count; -1 = at least one
		validateRemoved     func(t *testing.T, removed []v1alpha1.Reservation)
		validateTouched     func(t *testing.T, touched []v1alpha1.Reservation)
		validateRemaining   func(t *testing.T, remaining []v1alpha1.Reservation)
	}{
		{
			name:             "creates reservations to match desired memory",
			desiredMemoryGiB: 24, // 3 × 8 GiB slots
			validateTouched: func(t *testing.T, touched []v1alpha1.Reservation) {
				if len(touched) == 0 {
					t.Fatal("expected at least one reservation created")
				}
				var total int64
				for _, r := range touched {
					q := r.Spec.Resources[hv1.ResourceMemory]
					total += q.Value()
				}
				if want := int64(24 * 1024 * 1024 * 1024); total != want {
					t.Errorf("expected total memory %d, got %d", want, total)
				}
			},
		},
		{
			// Algorithm removes both 16 GiB slots and creates a new 8 GiB one.
			name: "removes excess reservations, remaining memory matches desired",
			existingSlots: []v1alpha1.Reservation{
				newTestCRSlot("commitment-abc123-0", 16, "", "test-group", nil),
				newTestCRSlot("commitment-abc123-1", 16, "", "test-group", nil),
			},
			desiredMemoryGiB: 8,
			wantRemovedCount: -1,
			validateRemaining: func(t *testing.T, remaining []v1alpha1.Reservation) {
				var total int64
				for _, r := range remaining {
					q := r.Spec.Resources[hv1.ResourceMemory]
					total += q.Value()
				}
				if want := int64(8 * 1024 * 1024 * 1024); total != want {
					t.Errorf("expected remaining memory %d, got %d", want, total)
				}
			},
		},
		{
			name: "zero desired memory removes all reservations",
			existingSlots: []v1alpha1.Reservation{
				newTestCRSlot("commitment-abc123-0", 8, "", "test-group", nil),
			},
			desiredMemoryGiB: 0,
			wantRemovedCount: 1,
			validateRemaining: func(t *testing.T, remaining []v1alpha1.Reservation) {
				if len(remaining) != 0 {
					t.Errorf("expected 0 remaining, got %d", len(remaining))
				}
			},
		},
		{
			name: "replaces reservation with wrong flavor group",
			existingSlots: []v1alpha1.Reservation{
				newTestCRSlot("commitment-abc123-0", 8, "", "wrong-group", nil),
			},
			desiredMemoryGiB: 8,
			wantRemovedCount: 1,
			validateTouched: func(t *testing.T, touched []v1alpha1.Reservation) {
				if len(touched) != 1 {
					t.Fatalf("expected 1 new reservation, got %d", len(touched))
				}
				if got := touched[0].Spec.CommittedResourceReservation.ResourceGroup; got != "test-group" {
					t.Errorf("expected flavor group test-group, got %s", got)
				}
			},
		},
		{
			name:                "unknown flavor group returns error",
			desiredMemoryGiB:    8,
			flavorGroupOverride: map[string]compute.FlavorGroupFeature{},
			wantError:           true,
		},
		{
			name: "deletion priority: unscheduled (no TargetHost) deleted before scheduled",
			existingSlots: []v1alpha1.Reservation{
				newTestCRSlot("commitment-abc123-0", 8, "host-1", "test-group", map[string]v1alpha1.CommittedResourceAllocation{"vm-123": {}}),
				newTestCRSlot("commitment-abc123-1", 8, "", "test-group", nil),
			},
			desiredMemoryGiB: 8,
			wantRemovedCount: 1,
			validateRemoved: func(t *testing.T, removed []v1alpha1.Reservation) {
				if removed[0].Spec.TargetHost != "" {
					t.Errorf("expected unscheduled reservation removed, got TargetHost=%q", removed[0].Spec.TargetHost)
				}
			},
			validateRemaining: func(t *testing.T, remaining []v1alpha1.Reservation) {
				if len(remaining) != 1 {
					t.Fatalf("expected 1 remaining, got %d", len(remaining))
				}
				if remaining[0].Spec.TargetHost == "" || len(remaining[0].Spec.CommittedResourceReservation.Allocations) == 0 {
					t.Error("expected scheduled reservation with allocations to remain")
				}
			},
		},
		{
			name: "deletion priority: unused scheduled (no allocations) deleted before allocated",
			existingSlots: []v1alpha1.Reservation{
				newTestCRSlot("commitment-abc123-0", 8, "host-1", "test-group", map[string]v1alpha1.CommittedResourceAllocation{"vm-123": {}}),
				newTestCRSlot("commitment-abc123-1", 8, "host-2", "test-group", nil),
			},
			desiredMemoryGiB: 8,
			wantRemovedCount: 1,
			validateRemoved: func(t *testing.T, removed []v1alpha1.Reservation) {
				if len(removed[0].Spec.CommittedResourceReservation.Allocations) != 0 {
					t.Error("expected reservation without allocations to be removed")
				}
			},
			validateRemaining: func(t *testing.T, remaining []v1alpha1.Reservation) {
				if len(remaining) != 1 {
					t.Fatalf("expected 1 remaining, got %d", len(remaining))
				}
				if len(remaining[0].Spec.CommittedResourceReservation.Allocations) == 0 {
					t.Error("expected reservation with allocations to remain")
				}
			},
		},
		{
			name: "deletion priority: unscheduled removed first across mixed set",
			existingSlots: []v1alpha1.Reservation{
				newTestCRSlot("commitment-abc123-0", 8, "host-1", "test-group", map[string]v1alpha1.CommittedResourceAllocation{"vm-allocated": {}}),
				newTestCRSlot("commitment-abc123-1", 8, "host-2", "test-group", nil),
				newTestCRSlot("commitment-abc123-2", 8, "", "test-group", nil),
				newTestCRSlot("commitment-abc123-3", 8, "", "test-group", nil),
			},
			desiredMemoryGiB: 16,
			wantRemovedCount: 2,
			validateRemoved: func(t *testing.T, removed []v1alpha1.Reservation) {
				for _, r := range removed {
					if r.Spec.TargetHost != "" {
						t.Errorf("expected unscheduled reservations removed first, got TargetHost=%q on %s", r.Spec.TargetHost, r.Name)
					}
				}
			},
			validateRemaining: func(t *testing.T, remaining []v1alpha1.Reservation) {
				if len(remaining) != 2 {
					t.Fatalf("expected 2 remaining, got %d", len(remaining))
				}
				for _, r := range remaining {
					if r.Spec.TargetHost == "" {
						t.Errorf("expected scheduled reservations to remain, got empty TargetHost on %s", r.Name)
					}
				}
			},
		},
	}

	scheme := newCRTestScheme(t)

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			objects := make([]client.Object, len(tt.existingSlots))
			for i := range tt.existingSlots {
				objects[i] = &tt.existingSlots[i]
			}
			k8sClient := newCRTestClient(scheme, objects...)
			manager := NewReservationManager(k8sClient)

			flavorGroups := testFlavorGroups()
			if tt.flavorGroupOverride != nil {
				flavorGroups = tt.flavorGroupOverride
			}
			desiredState := &CommitmentState{
				CommitmentUUID:   "abc123",
				ProjectID:        "project-1",
				FlavorGroupName:  "test-group",
				TotalMemoryBytes: tt.desiredMemoryGiB * 1024 * 1024 * 1024,
			}

			applyResult, err := manager.ApplyCommitmentState(
				context.Background(), logr.Discard(), desiredState, flavorGroups, "syncer",
			)

			if tt.wantError {
				if err == nil {
					t.Fatal("expected error, got nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			switch {
			case tt.wantRemovedCount > 0:
				if len(applyResult.RemovedReservations) != tt.wantRemovedCount {
					t.Fatalf("expected %d removed, got %d", tt.wantRemovedCount, len(applyResult.RemovedReservations))
				}
			case tt.wantRemovedCount == 0:
				if len(applyResult.RemovedReservations) != 0 {
					t.Errorf("expected 0 removed, got %d", len(applyResult.RemovedReservations))
				}
			case tt.wantRemovedCount == -1:
				if len(applyResult.RemovedReservations) == 0 {
					t.Fatal("expected at least one removed reservation")
				}
			}

			if tt.validateRemoved != nil {
				tt.validateRemoved(t, applyResult.RemovedReservations)
			}
			if tt.validateTouched != nil {
				tt.validateTouched(t, applyResult.TouchedReservations)
			}
			if tt.validateRemaining != nil {
				var remaining v1alpha1.ReservationList
				if err := k8sClient.List(context.Background(), &remaining); err != nil {
					t.Fatal(err)
				}
				tt.validateRemaining(t, remaining.Items)
			}
		})
	}
}

// ============================================================================
// Tests: newReservation flavor selection
// ============================================================================

func TestNewReservation_SelectsAppropriateFlavor(t *testing.T) {
	manager := &ReservationManager{}
	flavorGroup := testFlavorGroup()

	tests := []struct {
		name          string
		deltaMemory   int64
		expectedName  string
		expectedCores int64
	}{
		{
			name:          "fits large flavor",
			deltaMemory:   32768 * 1024 * 1024, // 32 GiB
			expectedName:  "large",
			expectedCores: 16,
		},
		{
			name:          "fits medium flavor",
			deltaMemory:   16384 * 1024 * 1024, // 16 GiB
			expectedName:  "medium",
			expectedCores: 8,
		},
		{
			name:          "fits small flavor",
			deltaMemory:   8192 * 1024 * 1024, // 8 GiB
			expectedName:  "small",
			expectedCores: 4,
		},
		{
			name:          "oversized uses largest available flavor",
			deltaMemory:   100 * 1024 * 1024 * 1024, // 100 GiB
			expectedName:  "large",
			expectedCores: 16,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			state := &CommitmentState{
				CommitmentUUID:   "test-uuid",
				ProjectID:        "project-1",
				FlavorGroupName:  "test-group",
				TotalMemoryBytes: tt.deltaMemory,
			}

			reservation := manager.newReservation(state, 0, tt.deltaMemory, flavorGroup, "syncer")

			if reservation.Spec.CommittedResourceReservation.ResourceName != tt.expectedName {
				t.Errorf("expected flavor %s, got %s",
					tt.expectedName, reservation.Spec.CommittedResourceReservation.ResourceName)
			}
			cpuQuantity := reservation.Spec.Resources[hv1.ResourceCPU]
			if cpuQuantity.Value() != tt.expectedCores {
				t.Errorf("expected %d cores, got %d", tt.expectedCores, cpuQuantity.Value())
			}
		})
	}
}

// variableRatioFlavorGroup returns a flavor group with varying CPU:RAM ratios (GP-style).
// Flavors are sorted descending by memory then vCPUs, matching the knowledge extractor order.
func variableRatioFlavorGroup() compute.FlavorGroupFeature {
	minRatio := uint64(2048) // MiB/vCPU
	maxRatio := uint64(8192) // MiB/vCPU
	return compute.FlavorGroupFeature{
		Name: "gp-group",
		Flavors: []compute.FlavorInGroup{
			{Name: "c4_m32", VCPUs: 4, MemoryMB: 32768, DiskGB: 100}, // 8 GiB/vCPU
			{Name: "c8_m16", VCPUs: 8, MemoryMB: 16384, DiskGB: 50},  // 2 GiB/vCPU
			{Name: "c4_m8", VCPUs: 4, MemoryMB: 8192, DiskGB: 25},    // 2 GiB/vCPU
		},
		SmallestFlavor:  compute.FlavorInGroup{Name: "c4_m8", VCPUs: 4, MemoryMB: 8192, DiskGB: 25},
		LargestFlavor:   compute.FlavorInGroup{Name: "c4_m32", VCPUs: 4, MemoryMB: 32768, DiskGB: 100},
		RamCoreRatioMin: &minRatio,
		RamCoreRatioMax: &maxRatio,
	}
}

func TestNewReservation_VariableRatioGroup_SelectsLargestByMemory(t *testing.T) {
	// For GP (variable CPU:RAM ratio) groups, flavor selection is driven by memory
	// descending, not by ratio. The largest flavor fitting the delta is always chosen.
	manager := &ReservationManager{}
	fg := variableRatioFlavorGroup()

	tests := []struct {
		name          string
		deltaMemoryMB int64
		wantFlavor    string
		wantCores     int64
	}{
		{
			name:          "delta fits c4_m32: picks largest by memory",
			deltaMemoryMB: 32768,
			wantFlavor:    "c4_m32",
			wantCores:     4,
		},
		{
			name:          "delta larger than all: picks largest (c4_m32)",
			deltaMemoryMB: 65536,
			wantFlavor:    "c4_m32",
			wantCores:     4,
		},
		{
			name:          "delta between c4_m32 and c8_m16: picks c8_m16",
			deltaMemoryMB: 24576, // 24 GiB — c8_m16 (16 GiB) fits, c4_m32 (32 GiB) doesn't
			wantFlavor:    "c8_m16",
			wantCores:     8,
		},
		{
			name:          "delta equals c8_m16: picks c8_m16 (more vCPUs than c4_m8 at same memory)",
			deltaMemoryMB: 16384,
			wantFlavor:    "c8_m16",
			wantCores:     8,
		},
		{
			name:          "delta fits only c4_m8: picks smallest",
			deltaMemoryMB: 8192,
			wantFlavor:    "c4_m8",
			wantCores:     4,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			deltaBytes := tt.deltaMemoryMB * 1024 * 1024
			state := &CommitmentState{
				CommitmentUUID:  "test-uuid",
				ProjectID:       "project-1",
				FlavorGroupName: "gp-group",
			}
			res := manager.newReservation(state, 0, deltaBytes, fg, "test")
			if got := res.Spec.CommittedResourceReservation.ResourceName; got != tt.wantFlavor {
				t.Errorf("flavor: want %s, got %s", tt.wantFlavor, got)
			}
			cpuQty := res.Spec.Resources[hv1.ResourceCPU]
			if got := cpuQty.Value(); got != tt.wantCores {
				t.Errorf("cores: want %d, got %d", tt.wantCores, got)
			}
		})
	}
}
