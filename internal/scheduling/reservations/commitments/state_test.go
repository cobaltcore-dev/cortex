// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package commitments

import (
	"testing"

	"github.com/cobaltcore-dev/cortex/api/v1alpha1"
	"github.com/cobaltcore-dev/cortex/internal/knowledge/extractor/plugins/compute"
	hv1 "github.com/cobaltcore-dev/openstack-hypervisor-operator/api/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// Test helper: creates a minimal flavor group for testing
func testFlavorGroup() compute.FlavorGroupFeature {
	return compute.FlavorGroupFeature{
		Name: "test-group",
		Flavors: []compute.FlavorInGroup{
			{Name: "large", VCPUs: 16, MemoryMB: 32768, DiskGB: 100},
			{Name: "medium", VCPUs: 8, MemoryMB: 16384, DiskGB: 50},
			{Name: "small", VCPUs: 4, MemoryMB: 8192, DiskGB: 25},
		},
		SmallestFlavor: compute.FlavorInGroup{
			Name: "small", VCPUs: 4, MemoryMB: 8192, DiskGB: 25,
		},
		LargestFlavor: compute.FlavorInGroup{
			Name: "large", VCPUs: 16, MemoryMB: 32768, DiskGB: 100,
		},
	}
}

func TestFromCommitment_CalculatesMemoryCorrectly(t *testing.T) {
	flavorGroup := testFlavorGroup()
	commitment := Commitment{
		UUID:         "test-uuid",
		ProjectID:    "project-1",
		ResourceName: "hw_version_test-group_ram",
		Amount:       5, // 5 multiples of smallest flavor
	}

	state, err := FromCommitment(commitment, flavorGroup)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Verify basic fields
	if state.CommitmentUUID != "test-uuid" {
		t.Errorf("expected UUID test-uuid, got %s", state.CommitmentUUID)
	}
	if state.ProjectID != "project-1" {
		t.Errorf("expected ProjectID project-1, got %s", state.ProjectID)
	}
	if state.FlavorGroupName != "test-group" {
		t.Errorf("expected FlavorGroupName test-group, got %s", state.FlavorGroupName)
	}

	// Verify memory calculation: 5 * 8192 MB = 40960 MB = 42949672960 bytes
	expectedMemory := int64(5 * 8192 * 1024 * 1024)
	if state.TotalMemoryBytes != expectedMemory {
		t.Errorf("expected memory %d, got %d", expectedMemory, state.TotalMemoryBytes)
	}
}

func TestFromCommitment_InvalidResourceName(t *testing.T) {
	flavorGroup := testFlavorGroup()
	commitment := Commitment{
		UUID:         "test-uuid",
		ProjectID:    "project-1",
		ResourceName: "invalid_resource_name", // missing "ram_" prefix
		Amount:       1,
	}

	_, err := FromCommitment(commitment, flavorGroup)
	if err == nil {
		t.Fatal("expected error for invalid resource name, got nil")
	}
}

func TestFromReservations_SumsMemoryCorrectly(t *testing.T) {
	reservations := []v1alpha1.Reservation{
		{
			ObjectMeta: metav1.ObjectMeta{
				Name: "commitment-abc123-0",
			},
			Spec: v1alpha1.ReservationSpec{
				Resources: map[hv1.ResourceName]resource.Quantity{
					hv1.ResourceMemory: *resource.NewQuantity(8*1024*1024*1024, resource.BinarySI), // 8 GiB
				},
				CommittedResourceReservation: &v1alpha1.CommittedResourceReservationSpec{
					ProjectID:     "project-1",
					ResourceGroup: "test-group",
				},
			},
		},
		{
			ObjectMeta: metav1.ObjectMeta{
				Name: "commitment-abc123-1",
			},
			Spec: v1alpha1.ReservationSpec{
				Resources: map[hv1.ResourceName]resource.Quantity{
					hv1.ResourceMemory: *resource.NewQuantity(16*1024*1024*1024, resource.BinarySI), // 16 GiB
				},
				CommittedResourceReservation: &v1alpha1.CommittedResourceReservationSpec{
					ProjectID:     "project-1",
					ResourceGroup: "test-group",
				},
			},
		},
	}

	state, err := FromReservations(reservations)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Verify fields extracted from first reservation
	if state.CommitmentUUID != "abc123" {
		t.Errorf("expected UUID abc123, got %s", state.CommitmentUUID)
	}
	if state.ProjectID != "project-1" {
		t.Errorf("expected ProjectID project-1, got %s", state.ProjectID)
	}
	if state.FlavorGroupName != "test-group" {
		t.Errorf("expected FlavorGroupName test-group, got %s", state.FlavorGroupName)
	}

	// Verify memory is summed correctly: 8 GiB + 16 GiB = 24 GiB
	expectedMemory := int64(24 * 1024 * 1024 * 1024)
	if state.TotalMemoryBytes != expectedMemory {
		t.Errorf("expected memory %d, got %d", expectedMemory, state.TotalMemoryBytes)
	}
}

func TestFromReservations_EmptyList(t *testing.T) {
	_, err := FromReservations([]v1alpha1.Reservation{})
	if err == nil {
		t.Fatal("expected error for empty reservation list, got nil")
	}
}

func TestFromReservations_SkipsInconsistentFlavorGroup(t *testing.T) {
	reservations := []v1alpha1.Reservation{
		{
			ObjectMeta: metav1.ObjectMeta{
				Name: "commitment-abc123-0",
			},
			Spec: v1alpha1.ReservationSpec{
				Resources: map[hv1.ResourceName]resource.Quantity{
					hv1.ResourceMemory: *resource.NewQuantity(8*1024*1024*1024, resource.BinarySI),
				},
				CommittedResourceReservation: &v1alpha1.CommittedResourceReservationSpec{
					ProjectID:     "project-1",
					ResourceGroup: "test-group",
				},
			},
		},
		{
			ObjectMeta: metav1.ObjectMeta{
				Name: "commitment-abc123-1",
			},
			Spec: v1alpha1.ReservationSpec{
				Resources: map[hv1.ResourceName]resource.Quantity{
					hv1.ResourceMemory: *resource.NewQuantity(16*1024*1024*1024, resource.BinarySI),
				},
				CommittedResourceReservation: &v1alpha1.CommittedResourceReservationSpec{
					ProjectID:     "project-1",
					ResourceGroup: "wrong-group", // Different flavor group
				},
			},
		},
	}

	state, err := FromReservations(reservations)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Should only count first reservation with matching flavor group
	expectedMemory := int64(8 * 1024 * 1024 * 1024)
	if state.TotalMemoryBytes != expectedMemory {
		t.Errorf("expected memory %d (ignoring inconsistent reservation), got %d",
			expectedMemory, state.TotalMemoryBytes)
	}
}

func TestFromReservations_MixedCommitmentUUIDs(t *testing.T) {
	reservations := []v1alpha1.Reservation{
		{
			ObjectMeta: metav1.ObjectMeta{
				Name: "commitment-abc123-0",
			},
			Spec: v1alpha1.ReservationSpec{
				CommittedResourceReservation: &v1alpha1.CommittedResourceReservationSpec{
					ProjectID:     "project-1",
					ResourceGroup: "test-group",
				},
			},
		},
		{
			ObjectMeta: metav1.ObjectMeta{
				Name: "commitment-xyz789-0", // Different commitment UUID
			},
			Spec: v1alpha1.ReservationSpec{
				CommittedResourceReservation: &v1alpha1.CommittedResourceReservationSpec{
					ProjectID:     "project-1",
					ResourceGroup: "test-group",
				},
			},
		},
	}

	_, err := FromReservations(reservations)
	if err == nil {
		t.Fatal("expected error for mixed commitment UUIDs, got nil")
	}
}

func TestFromReservations_NonCommittedResourceType(t *testing.T) {
	reservations := []v1alpha1.Reservation{
		{
			ObjectMeta: metav1.ObjectMeta{
				Name: "commitment-abc123-0",
			},
			Spec: v1alpha1.ReservationSpec{
				Type: v1alpha1.ReservationTypeFailover, // Wrong type
			},
		},
	}

	_, err := FromReservations(reservations)
	if err == nil {
		t.Fatal("expected error for non-CR reservation type, got nil")
	}
}

func TestGetFlavorGroupNameFromResource_Valid(t *testing.T) {
	// Test valid resource names with underscores in flavor group
	name, err := getFlavorGroupNameFromResource("hw_version_hana_medium_v2_ram")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if name != "hana_medium_v2" {
		t.Errorf("expected hana_medium_v2, got %s", name)
	}
}

func TestGetFlavorGroupNameFromResource_Invalid(t *testing.T) {
	invalidCases := []string{
		"ram_2101",        // old format
		"invalid",         // completely wrong
		"hw_version__ram", // empty group name
		"hw_version_2101", // missing suffix
	}
	for _, input := range invalidCases {
		if _, err := getFlavorGroupNameFromResource(input); err == nil {
			t.Errorf("expected error for %q, got nil", input)
		}
	}
}

func TestResourceNameRoundTrip(t *testing.T) {
	// Test that ResourceNameFromFlavorGroup and getFlavorGroupNameFromResource are inverses
	for _, groupName := range []string{"2101", "hana_1", "hana_medium_v2"} {
		resourceName := ResourceNameFromFlavorGroup(groupName)
		recovered, err := getFlavorGroupNameFromResource(resourceName)
		if err != nil {
			t.Fatalf("round-trip failed for %q: %v", groupName, err)
		}
		if recovered != groupName {
			t.Errorf("round-trip mismatch: %q -> %q -> %q", groupName, resourceName, recovered)
		}
	}
}
