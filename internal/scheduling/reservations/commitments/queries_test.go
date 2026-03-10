// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package commitments

import (
	"context"
	"testing"

	"github.com/cobaltcore-dev/cortex/api/v1alpha1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func TestListReservationsForCommitment_FindsByUUID(t *testing.T) {
	scheme := runtime.NewScheme()
	if err := v1alpha1.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}

	reservations := []v1alpha1.Reservation{
		{
			ObjectMeta: metav1.ObjectMeta{Name: "commitment-abc123-0"},
			Spec: v1alpha1.ReservationSpec{
				CommittedResourceReservation: &v1alpha1.CommittedResourceReservationSpec{
					ProjectID: "project-1",
					Creator:   "syncer",
				},
			},
		},
		{
			ObjectMeta: metav1.ObjectMeta{Name: "commitment-abc123-1"},
			Spec: v1alpha1.ReservationSpec{
				CommittedResourceReservation: &v1alpha1.CommittedResourceReservationSpec{
					ProjectID: "project-1",
					Creator:   "syncer",
				},
			},
		},
		{
			ObjectMeta: metav1.ObjectMeta{Name: "commitment-xyz789-0"},
			Spec: v1alpha1.ReservationSpec{
				CommittedResourceReservation: &v1alpha1.CommittedResourceReservationSpec{
					ProjectID: "project-2",
					Creator:   "syncer",
				},
			},
		},
	}

	client := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(
			&reservations[0],
			&reservations[1],
			&reservations[2],
		).
		Build()

	// Find reservations for abc123
	found, err := ListReservationsForCommitment(context.Background(), client, "abc123", "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(found) != 2 {
		t.Fatalf("expected 2 reservations, got %d", len(found))
	}

	// Verify both reservations belong to abc123
	for _, res := range found {
		if extractCommitmentUUID(res.Name) != "abc123" {
			t.Errorf("unexpected reservation %s", res.Name)
		}
	}
}

func TestListReservationsForCommitment_FiltersByCreator(t *testing.T) {
	scheme := runtime.NewScheme()
	if err := v1alpha1.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}

	reservations := []v1alpha1.Reservation{
		{
			ObjectMeta: metav1.ObjectMeta{Name: "commitment-abc123-0"},
			Spec: v1alpha1.ReservationSpec{
				CommittedResourceReservation: &v1alpha1.CommittedResourceReservationSpec{
					Creator: "syncer",
				},
			},
		},
		{
			ObjectMeta: metav1.ObjectMeta{Name: "commitment-abc123-1"},
			Spec: v1alpha1.ReservationSpec{
				CommittedResourceReservation: &v1alpha1.CommittedResourceReservationSpec{
					Creator: "api",
				},
			},
		},
	}

	client := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(&reservations[0], &reservations[1]).
		Build()

	// Find only syncer-created reservations
	found, err := ListReservationsForCommitment(context.Background(), client, "abc123", "syncer")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(found) != 1 {
		t.Fatalf("expected 1 reservation, got %d", len(found))
	}

	if found[0].Spec.CommittedResourceReservation.Creator != "syncer" {
		t.Errorf("expected creator syncer, got %s",
			found[0].Spec.CommittedResourceReservation.Creator)
	}
}

func TestListReservationsForCommitment_EmptyResult(t *testing.T) {
	scheme := runtime.NewScheme()
	if err := v1alpha1.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}

	client := fake.NewClientBuilder().
		WithScheme(scheme).
		Build()

	found, err := ListReservationsForCommitment(context.Background(), client, "nonexistent", "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(found) != 0 {
		t.Errorf("expected 0 reservations, got %d", len(found))
	}
}

func TestGetMaxSlotIndex_FindsHighestIndex(t *testing.T) {
	reservations := []v1alpha1.Reservation{
		{ObjectMeta: metav1.ObjectMeta{Name: "commitment-abc123-0"}},
		{ObjectMeta: metav1.ObjectMeta{Name: "commitment-abc123-5"}},
		{ObjectMeta: metav1.ObjectMeta{Name: "commitment-abc123-2"}},
	}

	maxIndex := GetMaxSlotIndex(reservations)
	if maxIndex != 5 {
		t.Errorf("expected max index 5, got %d", maxIndex)
	}
}

func TestGetMaxSlotIndex_EmptyList(t *testing.T) {
	maxIndex := GetMaxSlotIndex([]v1alpha1.Reservation{})
	if maxIndex != -1 {
		t.Errorf("expected -1 for empty list, got %d", maxIndex)
	}
}

func TestGetMaxSlotIndex_InvalidNames(t *testing.T) {
	reservations := []v1alpha1.Reservation{
		{ObjectMeta: metav1.ObjectMeta{Name: "invalid-name"}},
		{ObjectMeta: metav1.ObjectMeta{Name: "commitment-abc123"}}, // Missing slot index
	}

	maxIndex := GetMaxSlotIndex(reservations)
	if maxIndex != -1 {
		t.Errorf("expected -1 when no valid indices found, got %d", maxIndex)
	}
}

func TestGetNextSlotIndex_IncrementsByOne(t *testing.T) {
	reservations := []v1alpha1.Reservation{
		{ObjectMeta: metav1.ObjectMeta{Name: "commitment-abc123-0"}},
		{ObjectMeta: metav1.ObjectMeta{Name: "commitment-abc123-3"}},
	}

	nextIndex := GetNextSlotIndex(reservations)
	if nextIndex != 4 {
		t.Errorf("expected next index 4, got %d", nextIndex)
	}
}

func TestGetNextSlotIndex_EmptyList(t *testing.T) {
	nextIndex := GetNextSlotIndex([]v1alpha1.Reservation{})
	if nextIndex != 0 {
		t.Errorf("expected 0 for empty list, got %d", nextIndex)
	}
}

func TestExtractCommitmentUUID_SimpleUUID(t *testing.T) {
	uuid := extractCommitmentUUID("commitment-abc123-0")
	if uuid != "abc123" {
		t.Errorf("expected abc123, got %s", uuid)
	}
}

func TestExtractCommitmentUUID_ComplexUUID(t *testing.T) {
	// UUID with dashes (like standard UUID format)
	uuid := extractCommitmentUUID("commitment-550e8400-e29b-41d4-a716-446655440000-5")
	if uuid != "550e8400-e29b-41d4-a716-446655440000" {
		t.Errorf("expected full UUID, got %s", uuid)
	}
}

func TestExtractCommitmentUUID_NoSlotIndex(t *testing.T) {
	uuid := extractCommitmentUUID("commitment-abc123")
	if uuid != "abc123" {
		t.Errorf("expected abc123, got %s", uuid)
	}
}

func TestListReservationsForCommitment_IgnoresNonCRReservations(t *testing.T) {
	scheme := runtime.NewScheme()
	if err := v1alpha1.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}

	reservations := []v1alpha1.Reservation{
		{
			ObjectMeta: metav1.ObjectMeta{Name: "commitment-abc123-0"},
			Spec: v1alpha1.ReservationSpec{
				CommittedResourceReservation: &v1alpha1.CommittedResourceReservationSpec{
					ProjectID: "project-1",
				},
			},
		},
		{
			ObjectMeta: metav1.ObjectMeta{Name: "commitment-abc123-1"},
			Spec: v1alpha1.ReservationSpec{
				Type: v1alpha1.ReservationTypeFailover, // Non-CR type, no CommittedResourceReservation
			},
		},
	}

	client := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(&reservations[0], &reservations[1]).
		Build()

	found, err := ListReservationsForCommitment(context.Background(), client, "abc123", "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Should only find the CR reservation, not the HA one
	if len(found) != 1 {
		t.Fatalf("expected 1 reservation, got %d", len(found))
	}

	if found[0].Name != "commitment-abc123-0" {
		t.Errorf("expected commitment-abc123-0, got %s", found[0].Name)
	}
}

func TestListReservationsForCommitment_HandlesAllocations(t *testing.T) {
	scheme := runtime.NewScheme()
	if err := v1alpha1.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}

	// Create reservations with and without allocations
	reservations := []v1alpha1.Reservation{
		{
			ObjectMeta: metav1.ObjectMeta{Name: "commitment-abc123-0"},
			Spec: v1alpha1.ReservationSpec{
				Resources: map[string]resource.Quantity{
					"memory": *resource.NewQuantity(8*1024*1024*1024, resource.BinarySI),
				},
				CommittedResourceReservation: &v1alpha1.CommittedResourceReservationSpec{
					ProjectID: "project-1",
					Allocations: map[string]v1alpha1.CommittedResourceAllocation{
						"vm-123": {},
					},
				},
			},
		},
		{
			ObjectMeta: metav1.ObjectMeta{Name: "commitment-abc123-1"},
			Spec: v1alpha1.ReservationSpec{
				Resources: map[string]resource.Quantity{
					"memory": *resource.NewQuantity(8*1024*1024*1024, resource.BinarySI),
				},
				CommittedResourceReservation: &v1alpha1.CommittedResourceReservationSpec{
					ProjectID:   "project-1",
					Allocations: map[string]v1alpha1.CommittedResourceAllocation{}, // Empty
				},
			},
		},
	}

	client := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(&reservations[0], &reservations[1]).
		Build()

	found, err := ListReservationsForCommitment(context.Background(), client, "abc123", "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(found) != 2 {
		t.Fatalf("expected 2 reservations, got %d", len(found))
	}

	// Verify we can access allocation data
	hasAllocations := false
	for _, res := range found {
		if len(res.Spec.CommittedResourceReservation.Allocations) > 0 {
			hasAllocations = true
		}
	}

	if !hasAllocations {
		t.Error("expected at least one reservation with allocations")
	}
}
