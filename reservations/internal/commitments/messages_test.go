// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package commitments

import (
	"encoding/json"
	"reflect"
	"testing"
	"time"
)

func TestCommitment_JSONSerialization(t *testing.T) {
	now := uint64(time.Now().Unix())
	confirmBy := now + 3600
	confirmedAt := now + 7200

	testCommitment := Commitment{
		ID:               123,
		UUID:             "commitment-uuid-123",
		ServiceType:      "compute",
		ResourceName:     "instances_small",
		AvailabilityZone: "nova",
		Amount:           5,
		Unit:             "instances",
		Duration:         "1 year",
		CreatedAt:        now,
		ConfirmBy:        &confirmBy,
		ConfirmedAt:      &confirmedAt,
		ExpiresAt:        now + 31536000,
		Transferable:     true,
		Status:           "confirmed",
		NotifyOnConfirm:  true,
		ProjectID:        "project-123",
		DomainID:         "domain-456",
		Flavor: &Flavor{
			ID:          "flavor-1",
			Name:        "small",
			RAM:         1024,
			VCPUs:       1,
			Disk:        10,
			IsPublic:    true,
			RxTxFactor:  1.0,
			Ephemeral:   0,
			Description: "Small flavor",
			ExtraSpecs:  map[string]string{"hw:cpu_policy": "shared"},
		},
	}

	// Test marshaling
	jsonData, err := json.Marshal(testCommitment)
	if err != nil {
		t.Fatalf("Failed to marshal commitment: %v", err)
	}

	// Test unmarshaling
	var unmarshaledCommitment Commitment
	err = json.Unmarshal(jsonData, &unmarshaledCommitment)
	if err != nil {
		t.Fatalf("Failed to unmarshal commitment: %v", err)
	}

	// Verify all fields are preserved
	if unmarshaledCommitment.ID != testCommitment.ID {
		t.Errorf("Expected ID %d, got %d", testCommitment.ID, unmarshaledCommitment.ID)
	}
	if unmarshaledCommitment.UUID != testCommitment.UUID {
		t.Errorf("Expected UUID %s, got %s", testCommitment.UUID, unmarshaledCommitment.UUID)
	}
	if unmarshaledCommitment.ServiceType != testCommitment.ServiceType {
		t.Errorf("Expected ServiceType %s, got %s", testCommitment.ServiceType, unmarshaledCommitment.ServiceType)
	}
	if unmarshaledCommitment.ResourceName != testCommitment.ResourceName {
		t.Errorf("Expected ResourceName %s, got %s", testCommitment.ResourceName, unmarshaledCommitment.ResourceName)
	}
	if unmarshaledCommitment.Amount != testCommitment.Amount {
		t.Errorf("Expected Amount %d, got %d", testCommitment.Amount, unmarshaledCommitment.Amount)
	}
	if unmarshaledCommitment.CreatedAt != testCommitment.CreatedAt {
		t.Errorf("Expected CreatedAt %d, got %d", testCommitment.CreatedAt, unmarshaledCommitment.CreatedAt)
	}
	if unmarshaledCommitment.ExpiresAt != testCommitment.ExpiresAt {
		t.Errorf("Expected ExpiresAt %d, got %d", testCommitment.ExpiresAt, unmarshaledCommitment.ExpiresAt)
	}
	if unmarshaledCommitment.Transferable != testCommitment.Transferable {
		t.Errorf("Expected Transferable %t, got %t", testCommitment.Transferable, unmarshaledCommitment.Transferable)
	}
	if unmarshaledCommitment.NotifyOnConfirm != testCommitment.NotifyOnConfirm {
		t.Errorf("Expected NotifyOnConfirm %t, got %t", testCommitment.NotifyOnConfirm, unmarshaledCommitment.NotifyOnConfirm)
	}

	// Verify pointer fields
	if unmarshaledCommitment.ConfirmBy == nil {
		t.Error("Expected ConfirmBy to be non-nil")
	} else if *unmarshaledCommitment.ConfirmBy != *testCommitment.ConfirmBy {
		t.Errorf("Expected ConfirmBy %d, got %d", *testCommitment.ConfirmBy, *unmarshaledCommitment.ConfirmBy)
	}
	if unmarshaledCommitment.ConfirmedAt == nil {
		t.Error("Expected ConfirmedAt to be non-nil")
	} else if *unmarshaledCommitment.ConfirmedAt != *testCommitment.ConfirmedAt {
		t.Errorf("Expected ConfirmedAt %d, got %d", *testCommitment.ConfirmedAt, *unmarshaledCommitment.ConfirmedAt)
	}
}

func TestFlavor_JSONSerialization(t *testing.T) {
	testFlavor := Flavor{
		ID:          "flavor-test-123",
		Disk:        20,
		RAM:         2048,
		Name:        "medium",
		RxTxFactor:  1.5,
		VCPUs:       2,
		IsPublic:    true,
		Ephemeral:   10,
		Description: "Medium sized flavor for testing",
		ExtraSpecs: map[string]string{
			"hw:cpu_policy":                         "dedicated",
			"hw:numa_nodes":                         "2",
			"aggregate_instance_extra_specs:pinned": "true",
		},
	}

	// Test marshaling
	jsonData, err := json.Marshal(testFlavor)
	if err != nil {
		t.Fatalf("Failed to marshal flavor: %v", err)
	}

	// Test unmarshaling
	var unmarshaledFlavor Flavor
	err = json.Unmarshal(jsonData, &unmarshaledFlavor)
	if err != nil {
		t.Fatalf("Failed to unmarshal flavor: %v", err)
	}

	// Verify all fields are preserved
	if unmarshaledFlavor.ID != testFlavor.ID {
		t.Errorf("Expected ID %s, got %s", testFlavor.ID, unmarshaledFlavor.ID)
	}
	if unmarshaledFlavor.Name != testFlavor.Name {
		t.Errorf("Expected Name %s, got %s", testFlavor.Name, unmarshaledFlavor.Name)
	}
	if unmarshaledFlavor.RAM != testFlavor.RAM {
		t.Errorf("Expected RAM %d, got %d", testFlavor.RAM, unmarshaledFlavor.RAM)
	}
	if unmarshaledFlavor.VCPUs != testFlavor.VCPUs {
		t.Errorf("Expected VCPUs %d, got %d", testFlavor.VCPUs, unmarshaledFlavor.VCPUs)
	}
	if unmarshaledFlavor.Disk != testFlavor.Disk {
		t.Errorf("Expected Disk %d, got %d", testFlavor.Disk, unmarshaledFlavor.Disk)
	}
	if unmarshaledFlavor.RxTxFactor != testFlavor.RxTxFactor {
		t.Errorf("Expected RxTxFactor %f, got %f", testFlavor.RxTxFactor, unmarshaledFlavor.RxTxFactor)
	}
	if unmarshaledFlavor.IsPublic != testFlavor.IsPublic {
		t.Errorf("Expected IsPublic %t, got %t", testFlavor.IsPublic, unmarshaledFlavor.IsPublic)
	}
	if unmarshaledFlavor.Ephemeral != testFlavor.Ephemeral {
		t.Errorf("Expected Ephemeral %d, got %d", testFlavor.Ephemeral, unmarshaledFlavor.Ephemeral)
	}
	if unmarshaledFlavor.Description != testFlavor.Description {
		t.Errorf("Expected Description %s, got %s", testFlavor.Description, unmarshaledFlavor.Description)
	}

	// Verify ExtraSpecs map
	if !reflect.DeepEqual(unmarshaledFlavor.ExtraSpecs, testFlavor.ExtraSpecs) {
		t.Errorf("Expected ExtraSpecs %v, got %v", testFlavor.ExtraSpecs, unmarshaledFlavor.ExtraSpecs)
	}
}
