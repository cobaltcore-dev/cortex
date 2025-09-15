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
	if !reflect.DeepEqual(unmarshaledCommitment, testCommitment) {
		t.Errorf("Expected %+v, got %+v", testCommitment, unmarshaledCommitment)
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
