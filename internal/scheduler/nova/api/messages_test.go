// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package api

import (
	"encoding/json"
	"testing"
)

func TestNovaSpecUnmarshal(t *testing.T) {
	var jsonData = `{
        "spec": {
            "nova_object.name": "RequestSpec",
            "nova_object.namespace": "nova",
            "nova_object.version": "1.14",
            "nova_object.data": {
                "image": {
                    "nova_object.name": "ImageMeta",
                    "nova_object.namespace": "nova",
                    "nova_object.version": "1.8",
                    "nova_object.data": {
                        "name": "example-name",
                        "size": 123456789,
                        "min_ram": 2048,
                        "min_disk": 20
                    },
                    "nova_object.changes": ["id", "name", "size", "min_ram", "min_disk"]
                },
                "project_id": "example-project-id",
                "user_id": "example-user-id",
                "availability_zone": "example-az",
                "flavor": {
                    "nova_object.name": "Flavor",
                    "nova_object.namespace": "nova",
                    "nova_object.version": "1.2",
                    "nova_object.data": {
                        "name": "example-flavor-name",
                        "memory_mb": 4096,
                        "vcpus": 2,
                        "root_gb": 40,
                        "ephemeral_gb": 0,
                        "flavorid": "example-flavorid",
                        "swap": 0,
                        "rxtx_factor": 1.0,
                        "vcpu_weight": 0,
                        "extra_specs": {
                            "example-key": "example-value"
                        }
                    },
                    "nova_object.changes": ["id", "name", "memory_mb", "vcpus", "root_gb", "ephemeral_gb", "flavorid", "swap", "rxtx_factor", "vcpu_weight", "extra_specs"]
                },
                "num_instances": 1
            },
            "nova_object.changes": ["image", "project_id", "user_id", "availability_zone", "flavor", "num_instances"]
        }
    }`

	var spec struct {
		Spec NovaObject[NovaSpec] `json:"spec"`
	}
	err := json.Unmarshal([]byte(jsonData), &spec)
	if err != nil {
		t.Fatalf("Failed to unmarshal JSON: %v", err)
	}

	if spec.Spec.Data.ProjectID != "example-project-id" {
		t.Errorf("Expected ProjectID to be 'example-project-id', got '%s'", spec.Spec.Data.ProjectID)
	}
	if spec.Spec.Data.UserID != "example-user-id" {
		t.Errorf("Expected UserID to be 'example-user-id', got '%s'", spec.Spec.Data.UserID)
	}
	if spec.Spec.Data.AvailabilityZone != "example-az" {
		t.Errorf("Expected AvailabilityZone to be 'example-az', got '%s'", spec.Spec.Data.AvailabilityZone)
	}
	if spec.Spec.Data.NumInstances != 1 {
		t.Errorf("Expected NumInstances to be 1, got %d", spec.Spec.Data.NumInstances)
	}
}
