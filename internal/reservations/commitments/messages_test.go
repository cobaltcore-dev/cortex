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
	//nolint:gosec
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
	if !reflect.DeepEqual(unmarshaledFlavor, testFlavor) {
		t.Errorf("Expected %+v, got %+v", testFlavor, unmarshaledFlavor)
	}
}
