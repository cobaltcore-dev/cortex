// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package commitments

import (
	"testing"

	"github.com/cobaltcore-dev/cortex/api/v1alpha1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

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
