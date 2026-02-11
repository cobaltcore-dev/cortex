// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package api

import (
	"encoding/json"
	"testing"
)

func TestGetIntent(t *testing.T) {
	tests := []struct {
		name           string
		schedulerHints map[string]any
		expectedIntent RequestIntent
		expectError    bool
	}{
		{
			name: "rebuild intent",
			schedulerHints: map[string]any{
				"_nova_check_type": "rebuild",
			},
			expectedIntent: RebuildIntent,
			expectError:    false,
		},
		{
			name: "resize intent",
			schedulerHints: map[string]any{
				"_nova_check_type": "resize",
			},
			expectedIntent: ResizeIntent,
			expectError:    false,
		},
		{
			name: "live migration intent",
			schedulerHints: map[string]any{
				"_nova_check_type": "live_migrate",
			},
			expectedIntent: LiveMigrationIntent,
			expectError:    false,
		},
		{
			name: "evacuate intent",
			schedulerHints: map[string]any{
				"_nova_check_type": "evacuate",
			},
			expectedIntent: EvacuateIntent,
			expectError:    false,
		},
		{
			name: "create intent (default for unknown type)",
			schedulerHints: map[string]any{
				"_nova_check_type": "unknown_type",
			},
			expectedIntent: CreateIntent,
			expectError:    false,
		},
		{
			name: "create intent (default for empty string)",
			schedulerHints: map[string]any{
				"_nova_check_type": "",
			},
			expectedIntent: CreateIntent,
			expectError:    false,
		},
		{
			name: "rebuild intent from list hint",
			schedulerHints: map[string]any{
				"_nova_check_type": []any{"rebuild"},
			},
			expectedIntent: RebuildIntent,
			expectError:    false,
		},
		{
			name: "resize intent from list hint",
			schedulerHints: map[string]any{
				"_nova_check_type": []any{"resize"},
			},
			expectedIntent: ResizeIntent,
			expectError:    false,
		},
		{
			name: "live migration intent from list hint",
			schedulerHints: map[string]any{
				"_nova_check_type": []any{"live_migrate"},
			},
			expectedIntent: LiveMigrationIntent,
			expectError:    false,
		},
		{
			name: "evacuate intent from list hint",
			schedulerHints: map[string]any{
				"_nova_check_type": []any{"evacuate"},
			},
			expectedIntent: EvacuateIntent,
			expectError:    false,
		},
		{
			name:           "error when scheduler hints are nil",
			schedulerHints: nil,
			expectedIntent: "",
			expectError:    true,
		},
		{
			name:           "error when _nova_check_type key is missing",
			schedulerHints: map[string]any{},
			expectedIntent: "",
			expectError:    true,
		},
		{
			name: "error for unsupported hint type (int)",
			schedulerHints: map[string]any{
				"_nova_check_type": 123,
			},
			expectedIntent: "",
			expectError:    true,
		},
		{
			name: "error for empty list hint",
			schedulerHints: map[string]any{
				"_nova_check_type": []any{},
			},
			expectedIntent: "",
			expectError:    true,
		},
		{
			name: "error for list with non-string element",
			schedulerHints: map[string]any{
				"_nova_check_type": []any{123},
			},
			expectedIntent: "",
			expectError:    true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := ExternalSchedulerRequest{
				Spec: NovaObject[NovaSpec]{
					Data: NovaSpec{
						SchedulerHints: tt.schedulerHints,
					},
				},
			}

			intent, err := req.GetIntent()

			if tt.expectError {
				if err == nil {
					t.Errorf("expected error but got none")
				}
				return
			}

			if err != nil {
				t.Errorf("unexpected error: %v", err)
				return
			}

			if intent != tt.expectedIntent {
				t.Errorf("expected intent %q, got %q", tt.expectedIntent, intent)
			}
		})
	}
}

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
