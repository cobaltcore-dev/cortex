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
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func TestApplyCommitmentState_CreatesNewReservations(t *testing.T) {
	scheme := runtime.NewScheme()
	if err := v1alpha1.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}

	client := fake.NewClientBuilder().
		WithScheme(scheme).
		Build()

	manager := NewReservationManager(client)
	flavorGroup := testFlavorGroup()
	flavorGroups := map[string]compute.FlavorGroupFeature{
		"test-group": flavorGroup,
	}

	// Desired state: 3 multiples of smallest flavor (24 GiB)
	desiredState := &CommitmentState{
		CommitmentUUID:   "abc123",
		ProjectID:        "project-1",
		FlavorGroupName:  "test-group",
		TotalMemoryBytes: 3 * 8192 * 1024 * 1024,
	}

	touched, removed, err := manager.ApplyCommitmentState(
		context.Background(),
		logr.Discard(),
		desiredState,
		flavorGroups,
		"syncer",
	)

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(removed) != 0 {
		t.Errorf("expected 0 removed reservations, got %d", len(removed))
	}

	// Should create reservations to fulfill the commitment
	if len(touched) == 0 {
		t.Fatal("expected at least one reservation to be created")
	}

	// Verify created reservations sum to desired state
	totalMemory := int64(0)
	for _, res := range touched {
		memQuantity := res.Spec.Resources[hv1.ResourceMemory]
		totalMemory += memQuantity.Value()
	}

	if totalMemory != desiredState.TotalMemoryBytes {
		t.Errorf("expected total memory %d, got %d", desiredState.TotalMemoryBytes, totalMemory)
	}
}

func TestApplyCommitmentState_DeletesExcessReservations(t *testing.T) {
	scheme := runtime.NewScheme()
	if err := v1alpha1.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}

	// Create existing reservations (32 GiB total)
	existingReservations := []v1alpha1.Reservation{
		{
			ObjectMeta: metav1.ObjectMeta{
				Name: "commitment-abc123-0",
				Labels: map[string]string{
					v1alpha1.LabelReservationType: v1alpha1.ReservationTypeLabelCommittedResource,
				},
			},
			Spec: v1alpha1.ReservationSpec{
				Resources: map[hv1.ResourceName]resource.Quantity{
					hv1.ResourceMemory: *resource.NewQuantity(16*1024*1024*1024, resource.BinarySI),
				},
				CommittedResourceReservation: &v1alpha1.CommittedResourceReservationSpec{
					ProjectID:     "project-1",
					ResourceGroup: "test-group",
					Creator:       "syncer",
					Allocations:   map[string]v1alpha1.CommittedResourceAllocation{},
				},
			},
		},
		{
			ObjectMeta: metav1.ObjectMeta{
				Name: "commitment-abc123-1",
				Labels: map[string]string{
					v1alpha1.LabelReservationType: v1alpha1.ReservationTypeLabelCommittedResource,
				},
			},
			Spec: v1alpha1.ReservationSpec{
				Resources: map[hv1.ResourceName]resource.Quantity{
					hv1.ResourceMemory: *resource.NewQuantity(16*1024*1024*1024, resource.BinarySI),
				},
				CommittedResourceReservation: &v1alpha1.CommittedResourceReservationSpec{
					ProjectID:     "project-1",
					ResourceGroup: "test-group",
					Creator:       "syncer",
					Allocations:   map[string]v1alpha1.CommittedResourceAllocation{},
				},
			},
		},
	}

	client := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(&existingReservations[0], &existingReservations[1]).
		Build()

	manager := NewReservationManager(client)
	flavorGroup := testFlavorGroup()
	flavorGroups := map[string]compute.FlavorGroupFeature{
		"test-group": flavorGroup,
	}

	// Desired state: only 8 GiB (need to reduce)
	desiredState := &CommitmentState{
		CommitmentUUID:   "abc123",
		ProjectID:        "project-1",
		FlavorGroupName:  "test-group",
		TotalMemoryBytes: 8 * 1024 * 1024 * 1024,
	}

	_, removed, err := manager.ApplyCommitmentState(
		context.Background(),
		logr.Discard(),
		desiredState,
		flavorGroups,
		"syncer",
	)

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Note: May create a new 8GiB reservation while removing the two 16GiB ones
	// This is expected behavior based on the slot sizing algorithm

	// Should remove excess reservations
	if len(removed) == 0 {
		t.Fatal("expected reservations to be removed")
	}

	// Verify remaining capacity matches desired state
	var remainingList v1alpha1.ReservationList
	if err := client.List(context.Background(), &remainingList); err != nil {
		t.Fatal(err)
	}

	totalMemory := int64(0)
	for _, res := range remainingList.Items {
		memQuantity := res.Spec.Resources[hv1.ResourceMemory]
		totalMemory += memQuantity.Value()
	}

	if totalMemory != desiredState.TotalMemoryBytes {
		t.Errorf("expected remaining memory %d, got %d", desiredState.TotalMemoryBytes, totalMemory)
	}
}

func TestApplyCommitmentState_DeletionPriority(t *testing.T) {
	tests := []struct {
		name                 string
		existingReservations []v1alpha1.Reservation
		desiredMemoryBytes   int64
		expectedRemovedCount int
		validateRemoved      func(t *testing.T, removed []v1alpha1.Reservation)
		validateRemaining    func(t *testing.T, remaining []v1alpha1.Reservation)
	}{
		{
			name: "Priority 1: Unscheduled reservations (no TargetHost) deleted first",
			existingReservations: []v1alpha1.Reservation{
				// Reservation 0: Has TargetHost and allocations - lowest priority (should remain)
				{
					ObjectMeta: metav1.ObjectMeta{
						Name: "commitment-abc123-0",
						Labels: map[string]string{
							v1alpha1.LabelReservationType: v1alpha1.ReservationTypeLabelCommittedResource,
						},
					},
					Spec: v1alpha1.ReservationSpec{
						TargetHost: "host-1",
						Resources: map[hv1.ResourceName]resource.Quantity{
							hv1.ResourceMemory: *resource.NewQuantity(8*1024*1024*1024, resource.BinarySI),
						},
						CommittedResourceReservation: &v1alpha1.CommittedResourceReservationSpec{
							ProjectID:     "project-1",
							ResourceGroup: "test-group",
							Creator:       "syncer",
							Allocations: map[string]v1alpha1.CommittedResourceAllocation{
								"vm-123": {},
							},
						},
					},
				},
				// Reservation 1: No TargetHost and no allocations - highest priority (should be deleted)
				{
					ObjectMeta: metav1.ObjectMeta{
						Name: "commitment-abc123-1",
						Labels: map[string]string{
							v1alpha1.LabelReservationType: v1alpha1.ReservationTypeLabelCommittedResource,
						},
					},
					Spec: v1alpha1.ReservationSpec{
						TargetHost: "",
						Resources: map[hv1.ResourceName]resource.Quantity{
							hv1.ResourceMemory: *resource.NewQuantity(8*1024*1024*1024, resource.BinarySI),
						},
						CommittedResourceReservation: &v1alpha1.CommittedResourceReservationSpec{
							ProjectID:     "project-1",
							ResourceGroup: "test-group",
							Creator:       "syncer",
							Allocations:   map[string]v1alpha1.CommittedResourceAllocation{},
						},
					},
				},
			},
			desiredMemoryBytes:   8 * 1024 * 1024 * 1024, // Need to delete one
			expectedRemovedCount: 1,
			validateRemoved: func(t *testing.T, removed []v1alpha1.Reservation) {
				// Should have removed the unscheduled one (no TargetHost)
				if removed[0].Spec.TargetHost != "" {
					t.Errorf("expected unscheduled reservation to be removed, but removed %s with TargetHost %s",
						removed[0].Name, removed[0].Spec.TargetHost)
				}
			},
			validateRemaining: func(t *testing.T, remaining []v1alpha1.Reservation) {
				if len(remaining) != 1 {
					t.Fatalf("expected 1 remaining reservation, got %d", len(remaining))
				}
				// Should have kept the scheduled one with allocations
				if remaining[0].Spec.TargetHost == "" {
					t.Error("expected scheduled reservation to remain")
				}
				if len(remaining[0].Spec.CommittedResourceReservation.Allocations) == 0 {
					t.Error("expected reservation with allocations to remain")
				}
			},
		},
		{
			name: "Priority 2: Unused scheduled reservations (no allocations) deleted next",
			existingReservations: []v1alpha1.Reservation{
				// Has TargetHost AND allocations - lowest priority for deletion
				{
					ObjectMeta: metav1.ObjectMeta{
						Name: "commitment-abc123-0",
						Labels: map[string]string{
							v1alpha1.LabelReservationType: v1alpha1.ReservationTypeLabelCommittedResource,
						},
					},
					Spec: v1alpha1.ReservationSpec{
						TargetHost: "host-1",
						Resources: map[hv1.ResourceName]resource.Quantity{
							hv1.ResourceMemory: *resource.NewQuantity(8*1024*1024*1024, resource.BinarySI),
						},
						CommittedResourceReservation: &v1alpha1.CommittedResourceReservationSpec{
							ProjectID:     "project-1",
							ResourceGroup: "test-group",
							Creator:       "syncer",
							Allocations: map[string]v1alpha1.CommittedResourceAllocation{
								"vm-123": {},
							},
						},
					},
				},
				// Has TargetHost but NO allocations - medium priority
				{
					ObjectMeta: metav1.ObjectMeta{
						Name: "commitment-abc123-1",
						Labels: map[string]string{
							v1alpha1.LabelReservationType: v1alpha1.ReservationTypeLabelCommittedResource,
						},
					},
					Spec: v1alpha1.ReservationSpec{
						TargetHost: "host-2",
						Resources: map[hv1.ResourceName]resource.Quantity{
							hv1.ResourceMemory: *resource.NewQuantity(8*1024*1024*1024, resource.BinarySI),
						},
						CommittedResourceReservation: &v1alpha1.CommittedResourceReservationSpec{
							ProjectID:     "project-1",
							ResourceGroup: "test-group",
							Creator:       "syncer",
							Allocations:   map[string]v1alpha1.CommittedResourceAllocation{},
						},
					},
				},
			},
			desiredMemoryBytes:   8 * 1024 * 1024 * 1024,
			expectedRemovedCount: 1,
			validateRemoved: func(t *testing.T, removed []v1alpha1.Reservation) {
				// Should have removed the one without allocations
				if len(removed[0].Spec.CommittedResourceReservation.Allocations) != 0 {
					t.Error("expected reservation without allocations to be removed")
				}
			},
			validateRemaining: func(t *testing.T, remaining []v1alpha1.Reservation) {
				if len(remaining) != 1 {
					t.Fatalf("expected 1 remaining reservation, got %d", len(remaining))
				}
				// Should have kept the one with allocations
				if len(remaining[0].Spec.CommittedResourceReservation.Allocations) == 0 {
					t.Error("expected reservation with allocations to remain")
				}
			},
		},
		{
			name: "Mixed scenario: comprehensive deletion priority test",
			existingReservations: []v1alpha1.Reservation{
				// Reservation 0: Has TargetHost + has allocations (lowest priority - should remain)
				{
					ObjectMeta: metav1.ObjectMeta{
						Name: "commitment-abc123-0",
						Labels: map[string]string{
							v1alpha1.LabelReservationType: v1alpha1.ReservationTypeLabelCommittedResource,
						},
					},
					Spec: v1alpha1.ReservationSpec{
						TargetHost: "host-1",
						Resources: map[hv1.ResourceName]resource.Quantity{
							hv1.ResourceMemory: *resource.NewQuantity(8*1024*1024*1024, resource.BinarySI),
						},
						CommittedResourceReservation: &v1alpha1.CommittedResourceReservationSpec{
							ProjectID:     "project-1",
							ResourceGroup: "test-group",
							Creator:       "syncer",
							Allocations: map[string]v1alpha1.CommittedResourceAllocation{
								"vm-allocated": {},
							},
						},
					},
				},
				// Reservation 1: Has TargetHost + no allocations (medium priority - should remain)
				{
					ObjectMeta: metav1.ObjectMeta{
						Name: "commitment-abc123-1",
						Labels: map[string]string{
							v1alpha1.LabelReservationType: v1alpha1.ReservationTypeLabelCommittedResource,
						},
					},
					Spec: v1alpha1.ReservationSpec{
						TargetHost: "host-2",
						Resources: map[hv1.ResourceName]resource.Quantity{
							hv1.ResourceMemory: *resource.NewQuantity(8*1024*1024*1024, resource.BinarySI),
						},
						CommittedResourceReservation: &v1alpha1.CommittedResourceReservationSpec{
							ProjectID:     "project-1",
							ResourceGroup: "test-group",
							Creator:       "syncer",
							Allocations:   map[string]v1alpha1.CommittedResourceAllocation{},
						},
					},
				},
				// Reservation 2: No TargetHost + no allocations (highest priority - should be deleted)
				{
					ObjectMeta: metav1.ObjectMeta{
						Name: "commitment-abc123-2",
						Labels: map[string]string{
							v1alpha1.LabelReservationType: v1alpha1.ReservationTypeLabelCommittedResource,
						},
					},
					Spec: v1alpha1.ReservationSpec{
						TargetHost: "",
						Resources: map[hv1.ResourceName]resource.Quantity{
							hv1.ResourceMemory: *resource.NewQuantity(8*1024*1024*1024, resource.BinarySI),
						},
						CommittedResourceReservation: &v1alpha1.CommittedResourceReservationSpec{
							ProjectID:     "project-1",
							ResourceGroup: "test-group",
							Creator:       "syncer",
							Allocations:   map[string]v1alpha1.CommittedResourceAllocation{},
						},
					},
				},
				// Reservation 3: No TargetHost + no allocations (highest priority - should be deleted)
				{
					ObjectMeta: metav1.ObjectMeta{
						Name: "commitment-abc123-3",
						Labels: map[string]string{
							v1alpha1.LabelReservationType: v1alpha1.ReservationTypeLabelCommittedResource,
						},
					},
					Spec: v1alpha1.ReservationSpec{
						TargetHost: "",
						Resources: map[hv1.ResourceName]resource.Quantity{
							hv1.ResourceMemory: *resource.NewQuantity(8*1024*1024*1024, resource.BinarySI),
						},
						CommittedResourceReservation: &v1alpha1.CommittedResourceReservationSpec{
							ProjectID:     "project-1",
							ResourceGroup: "test-group",
							Creator:       "syncer",
							Allocations:   map[string]v1alpha1.CommittedResourceAllocation{},
						},
					},
				},
			},
			desiredMemoryBytes:   16 * 1024 * 1024 * 1024, // Need to delete 2 out of 4
			expectedRemovedCount: 2,
			validateRemoved: func(t *testing.T, removed []v1alpha1.Reservation) {
				// Both removed should have no TargetHost (highest priority for deletion)
				for _, res := range removed {
					if res.Spec.TargetHost != "" {
						t.Errorf("expected unscheduled reservations to be removed first, but removed %s with TargetHost %s",
							res.Name, res.Spec.TargetHost)
					}
				}
			},
			validateRemaining: func(t *testing.T, remaining []v1alpha1.Reservation) {
				if len(remaining) != 2 {
					t.Fatalf("expected 2 remaining reservations, got %d", len(remaining))
				}
				// Both remaining should have TargetHost
				for _, res := range remaining {
					if res.Spec.TargetHost == "" {
						t.Errorf("expected scheduled reservations to remain, but %s has no TargetHost", res.Name)
					}
				}
				// At least one should have allocations (the one with lowest deletion priority)
				hasAllocations := false
				for _, res := range remaining {
					if len(res.Spec.CommittedResourceReservation.Allocations) > 0 {
						hasAllocations = true
						break
					}
				}
				if !hasAllocations {
					t.Error("expected at least one remaining reservation to have allocations")
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			scheme := runtime.NewScheme()
			if err := v1alpha1.AddToScheme(scheme); err != nil {
				t.Fatal(err)
			}

			// Convert slice to individual objects for WithObjects
			objects := make([]client.Object, len(tt.existingReservations))
			for i := range tt.existingReservations {
				objects[i] = &tt.existingReservations[i]
			}

			client := fake.NewClientBuilder().
				WithScheme(scheme).
				WithObjects(objects...).
				Build()

			manager := NewReservationManager(client)
			flavorGroup := testFlavorGroup()
			flavorGroups := map[string]compute.FlavorGroupFeature{
				"test-group": flavorGroup,
			}

			desiredState := &CommitmentState{
				CommitmentUUID:   "abc123",
				ProjectID:        "project-1",
				FlavorGroupName:  "test-group",
				TotalMemoryBytes: tt.desiredMemoryBytes,
			}

			_, removed, err := manager.ApplyCommitmentState(
				context.Background(),
				logr.Discard(),
				desiredState,
				flavorGroups,
				"syncer",
			)

			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			if len(removed) != tt.expectedRemovedCount {
				t.Fatalf("expected %d removed reservations, got %d", tt.expectedRemovedCount, len(removed))
			}

			if tt.validateRemoved != nil {
				tt.validateRemoved(t, removed)
			}

			// Get remaining reservations
			var remainingList v1alpha1.ReservationList
			if err := client.List(context.Background(), &remainingList); err != nil {
				t.Fatal(err)
			}

			if tt.validateRemaining != nil {
				tt.validateRemaining(t, remainingList.Items)
			}
		})
	}
}

func TestApplyCommitmentState_HandlesZeroCapacity(t *testing.T) {
	scheme := runtime.NewScheme()
	if err := v1alpha1.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}

	// Create existing reservation
	existingReservation := v1alpha1.Reservation{
		ObjectMeta: metav1.ObjectMeta{
			Name: "commitment-abc123-0",
			Labels: map[string]string{
				v1alpha1.LabelReservationType: v1alpha1.ReservationTypeLabelCommittedResource,
			},
		},
		Spec: v1alpha1.ReservationSpec{
			Resources: map[hv1.ResourceName]resource.Quantity{
				hv1.ResourceMemory: *resource.NewQuantity(8*1024*1024*1024, resource.BinarySI),
			},
			CommittedResourceReservation: &v1alpha1.CommittedResourceReservationSpec{
				ProjectID:     "project-1",
				ResourceGroup: "test-group",
				Creator:       "syncer",
				Allocations:   map[string]v1alpha1.CommittedResourceAllocation{},
			},
		},
	}

	client := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(&existingReservation).
		Build()

	manager := NewReservationManager(client)
	flavorGroup := testFlavorGroup()
	flavorGroups := map[string]compute.FlavorGroupFeature{
		"test-group": flavorGroup,
	}

	// Desired state: zero capacity (commitment expired or canceled)
	desiredState := &CommitmentState{
		CommitmentUUID:   "abc123",
		ProjectID:        "project-1",
		FlavorGroupName:  "test-group",
		TotalMemoryBytes: 0,
	}

	touched, removed, err := manager.ApplyCommitmentState(
		context.Background(),
		logr.Discard(),
		desiredState,
		flavorGroups,
		"syncer",
	)

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(touched) != 0 {
		t.Errorf("expected 0 new reservations, got %d", len(touched))
	}

	// Should remove all reservations
	if len(removed) != 1 {
		t.Fatalf("expected 1 removed reservation, got %d", len(removed))
	}

	// Verify no reservations remain
	var remainingList v1alpha1.ReservationList
	if err := client.List(context.Background(), &remainingList); err != nil {
		t.Fatal(err)
	}

	if len(remainingList.Items) != 0 {
		t.Errorf("expected 0 remaining reservations, got %d", len(remainingList.Items))
	}
}

func TestApplyCommitmentState_FixesWrongFlavorGroup(t *testing.T) {
	scheme := runtime.NewScheme()
	if err := v1alpha1.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}

	// Create reservation with wrong flavor group
	existingReservation := v1alpha1.Reservation{
		ObjectMeta: metav1.ObjectMeta{
			Name: "commitment-abc123-0",
			Labels: map[string]string{
				v1alpha1.LabelReservationType: v1alpha1.ReservationTypeLabelCommittedResource,
			},
		},
		Spec: v1alpha1.ReservationSpec{
			Resources: map[hv1.ResourceName]resource.Quantity{
				hv1.ResourceMemory: *resource.NewQuantity(8*1024*1024*1024, resource.BinarySI),
			},
			CommittedResourceReservation: &v1alpha1.CommittedResourceReservationSpec{
				ProjectID:     "project-1",
				ResourceGroup: "wrong-group", // Wrong flavor group
				Creator:       "syncer",
				Allocations:   map[string]v1alpha1.CommittedResourceAllocation{},
			},
		},
	}

	client := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(&existingReservation).
		Build()

	manager := NewReservationManager(client)
	flavorGroup := testFlavorGroup()
	flavorGroups := map[string]compute.FlavorGroupFeature{
		"test-group": flavorGroup,
	}

	// Desired state with correct flavor group
	desiredState := &CommitmentState{
		CommitmentUUID:   "abc123",
		ProjectID:        "project-1",
		FlavorGroupName:  "test-group",
		TotalMemoryBytes: 8 * 1024 * 1024 * 1024,
	}

	touched, removed, err := manager.ApplyCommitmentState(
		context.Background(),
		logr.Discard(),
		desiredState,
		flavorGroups,
		"syncer",
	)

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Should remove wrong reservation and create new one
	if len(removed) != 1 {
		t.Fatalf("expected 1 removed reservation, got %d", len(removed))
	}

	if len(touched) != 1 {
		t.Fatalf("expected 1 new reservation, got %d", len(touched))
	}

	// Verify new reservation has correct flavor group
	if touched[0].Spec.CommittedResourceReservation.ResourceGroup != "test-group" {
		t.Errorf("expected flavor group test-group, got %s",
			touched[0].Spec.CommittedResourceReservation.ResourceGroup)
	}
}

func TestApplyCommitmentState_UnknownFlavorGroup(t *testing.T) {
	scheme := runtime.NewScheme()
	if err := v1alpha1.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}

	client := fake.NewClientBuilder().
		WithScheme(scheme).
		Build()

	manager := NewReservationManager(client)
	flavorGroups := map[string]compute.FlavorGroupFeature{} // Empty

	desiredState := &CommitmentState{
		CommitmentUUID:   "abc123",
		ProjectID:        "project-1",
		FlavorGroupName:  "unknown-group",
		TotalMemoryBytes: 8 * 1024 * 1024 * 1024,
	}

	_, _, err := manager.ApplyCommitmentState(
		context.Background(),
		logr.Discard(),
		desiredState,
		flavorGroups,
		"syncer",
	)

	if err == nil {
		t.Fatal("expected error for unknown flavor group, got nil")
	}
}

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
			deltaMemory:   100 * 1024 * 1024 * 1024, // 100 GiB (larger than any flavor)
			expectedName:  "large",                  // Will use largest available
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

			reservation := manager.newReservation(
				state,
				0,
				tt.deltaMemory,
				flavorGroup,
				"syncer",
			)

			// Verify flavor selection
			if reservation.Spec.CommittedResourceReservation.ResourceName != tt.expectedName {
				t.Errorf("expected flavor %s, got %s",
					tt.expectedName,
					reservation.Spec.CommittedResourceReservation.ResourceName)
			}

			// Verify CPU allocation
			cpuQuantity := reservation.Spec.Resources[hv1.ResourceCPU]
			if cpuQuantity.Value() != tt.expectedCores {
				t.Errorf("expected %d cores, got %d",
					tt.expectedCores, cpuQuantity.Value())
			}
		})
	}
}
