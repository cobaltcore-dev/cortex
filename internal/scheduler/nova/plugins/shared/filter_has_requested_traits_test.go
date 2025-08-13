// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package shared

import (
	"log/slog"
	"testing"

	"github.com/cobaltcore-dev/cortex/internal/conf"
	"github.com/cobaltcore-dev/cortex/internal/db"
	"github.com/cobaltcore-dev/cortex/internal/extractor/plugins/shared"
	"github.com/cobaltcore-dev/cortex/internal/scheduler/nova/api"
	testlibDB "github.com/cobaltcore-dev/cortex/testlib/db"
)

func TestFilterHasRequestedTraits_Run(t *testing.T) {
	dbEnv := testlibDB.SetupDBEnv(t)
	testDB := db.DB{DbMap: dbEnv.DbMap}
	defer testDB.Close()
	defer dbEnv.Close()

	// Create dependency tables
	err := testDB.CreateTable(
		testDB.AddTable(shared.HostCapabilities{}),
	)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	// Insert mock data into the feature_host_capabilities table
	_, err = testDB.Exec(`
		INSERT INTO feature_host_capabilities (compute_host, traits, hypervisor_type)
		VALUES
			('host1', 'COMPUTE_ACCELERATORS,COMPUTE_NET_VIRTIO_PACKED,CUSTOM_GPU_NVIDIA', 'QEMU'),
			('host2', 'COMPUTE_STATUS_ENABLED,COMPUTE_NET_VIRTIO', 'QEMU'),
			('host3', 'COMPUTE_ACCELERATORS,COMPUTE_STATUS_ENABLED,CUSTOM_STORAGE_SSD', 'VMware'),
			('host4', 'COMPUTE_NET_VIRTIO_PACKED,CUSTOM_CPU_AVX512', 'QEMU'),
			('host5', '', 'QEMU'),
			('host6', 'COMPUTE_ACCELERATORS,CUSTOM_GPU_AMD,CUSTOM_STORAGE_NVME', 'QEMU')
	`)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	tests := []struct {
		name          string
		request       api.ExternalSchedulerRequest
		expectedHosts []string
		filteredHosts []string
	}{
		{
			name: "No traits requested - no filtering",
			request: api.ExternalSchedulerRequest{
				Spec: api.NovaObject[api.NovaSpec]{
					Data: api.NovaSpec{
						Flavor: api.NovaObject[api.NovaFlavor]{
							Data: api.NovaFlavor{
								ExtraSpecs: map[string]string{
									"hw:cpu_policy": "dedicated",
								},
							},
						},
					},
				},
				Hosts: []api.ExternalSchedulerHost{
					{ComputeHost: "host1"},
					{ComputeHost: "host2"},
					{ComputeHost: "host3"},
					{ComputeHost: "host4"},
					{ComputeHost: "host5"},
					{ComputeHost: "host6"},
				},
			},
			expectedHosts: []string{"host1", "host2", "host3", "host4", "host5", "host6"},
			filteredHosts: []string{},
		},
		{
			name: "Single required trait - filter hosts without it",
			request: api.ExternalSchedulerRequest{
				Spec: api.NovaObject[api.NovaSpec]{
					Data: api.NovaSpec{
						Flavor: api.NovaObject[api.NovaFlavor]{
							Data: api.NovaFlavor{
								ExtraSpecs: map[string]string{
									"trait:COMPUTE_ACCELERATORS": "required",
								},
							},
						},
					},
				},
				Hosts: []api.ExternalSchedulerHost{
					{ComputeHost: "host1"},
					{ComputeHost: "host2"},
					{ComputeHost: "host3"},
					{ComputeHost: "host4"},
					{ComputeHost: "host5"},
					{ComputeHost: "host6"},
				},
			},
			expectedHosts: []string{"host1", "host3", "host6"}, // Only hosts with COMPUTE_ACCELERATORS
			filteredHosts: []string{"host2", "host4", "host5"}, // Hosts without COMPUTE_ACCELERATORS
		},
		{
			name: "Single forbidden trait - filter hosts with it",
			request: api.ExternalSchedulerRequest{
				Spec: api.NovaObject[api.NovaSpec]{
					Data: api.NovaSpec{
						Flavor: api.NovaObject[api.NovaFlavor]{
							Data: api.NovaFlavor{
								ExtraSpecs: map[string]string{
									"trait:COMPUTE_ACCELERATORS": "forbidden",
								},
							},
						},
					},
				},
				Hosts: []api.ExternalSchedulerHost{
					{ComputeHost: "host1"},
					{ComputeHost: "host2"},
					{ComputeHost: "host3"},
					{ComputeHost: "host4"},
					{ComputeHost: "host5"},
					{ComputeHost: "host6"},
				},
			},
			expectedHosts: []string{"host2", "host4", "host5"}, // Hosts without COMPUTE_ACCELERATORS
			filteredHosts: []string{"host1", "host3", "host6"}, // Hosts with COMPUTE_ACCELERATORS
		},
		{
			name: "Multiple required traits - filter hosts missing any",
			request: api.ExternalSchedulerRequest{
				Spec: api.NovaObject[api.NovaSpec]{
					Data: api.NovaSpec{
						Flavor: api.NovaObject[api.NovaFlavor]{
							Data: api.NovaFlavor{
								ExtraSpecs: map[string]string{
									"trait:COMPUTE_ACCELERATORS":      "required",
									"trait:COMPUTE_NET_VIRTIO_PACKED": "required",
								},
							},
						},
					},
				},
				Hosts: []api.ExternalSchedulerHost{
					{ComputeHost: "host1"},
					{ComputeHost: "host2"},
					{ComputeHost: "host3"},
					{ComputeHost: "host4"},
					{ComputeHost: "host5"},
					{ComputeHost: "host6"},
				},
			},
			expectedHosts: []string{"host1"}, // Only host1 has both traits
			filteredHosts: []string{"host2", "host3", "host4", "host5", "host6"},
		},
		{
			name: "Multiple forbidden traits - filter hosts with any",
			request: api.ExternalSchedulerRequest{
				Spec: api.NovaObject[api.NovaSpec]{
					Data: api.NovaSpec{
						Flavor: api.NovaObject[api.NovaFlavor]{
							Data: api.NovaFlavor{
								ExtraSpecs: map[string]string{
									"trait:COMPUTE_ACCELERATORS": "forbidden",
									"trait:CUSTOM_CPU_AVX512":    "forbidden",
								},
							},
						},
					},
				},
				Hosts: []api.ExternalSchedulerHost{
					{ComputeHost: "host1"},
					{ComputeHost: "host2"},
					{ComputeHost: "host3"},
					{ComputeHost: "host4"},
					{ComputeHost: "host5"},
					{ComputeHost: "host6"},
				},
			},
			expectedHosts: []string{"host2", "host5"}, // Hosts without any forbidden traits
			filteredHosts: []string{"host1", "host3", "host4", "host6"},
		},
		{
			name: "Mixed required and forbidden traits",
			request: api.ExternalSchedulerRequest{
				Spec: api.NovaObject[api.NovaSpec]{
					Data: api.NovaSpec{
						Flavor: api.NovaObject[api.NovaFlavor]{
							Data: api.NovaFlavor{
								ExtraSpecs: map[string]string{
									"trait:COMPUTE_STATUS_ENABLED": "required",
									"trait:COMPUTE_ACCELERATORS":   "forbidden",
								},
							},
						},
					},
				},
				Hosts: []api.ExternalSchedulerHost{
					{ComputeHost: "host1"},
					{ComputeHost: "host2"},
					{ComputeHost: "host3"},
					{ComputeHost: "host4"},
					{ComputeHost: "host5"},
					{ComputeHost: "host6"},
				},
			},
			expectedHosts: []string{"host2"}, // Only host2 has required trait and not forbidden trait
			filteredHosts: []string{"host1", "host3", "host4", "host5", "host6"},
		},
		{
			name: "Custom traits - required",
			request: api.ExternalSchedulerRequest{
				Spec: api.NovaObject[api.NovaSpec]{
					Data: api.NovaSpec{
						Flavor: api.NovaObject[api.NovaFlavor]{
							Data: api.NovaFlavor{
								ExtraSpecs: map[string]string{
									"trait:CUSTOM_GPU_NVIDIA": "required",
								},
							},
						},
					},
				},
				Hosts: []api.ExternalSchedulerHost{
					{ComputeHost: "host1"},
					{ComputeHost: "host2"},
					{ComputeHost: "host3"},
					{ComputeHost: "host4"},
					{ComputeHost: "host5"},
					{ComputeHost: "host6"},
				},
			},
			expectedHosts: []string{"host1"}, // Only host1 has CUSTOM_GPU_NVIDIA
			filteredHosts: []string{"host2", "host3", "host4", "host5", "host6"},
		},
		{
			name: "Custom traits - forbidden",
			request: api.ExternalSchedulerRequest{
				Spec: api.NovaObject[api.NovaSpec]{
					Data: api.NovaSpec{
						Flavor: api.NovaObject[api.NovaFlavor]{
							Data: api.NovaFlavor{
								ExtraSpecs: map[string]string{
									"trait:CUSTOM_STORAGE_SSD": "forbidden",
								},
							},
						},
					},
				},
				Hosts: []api.ExternalSchedulerHost{
					{ComputeHost: "host1"},
					{ComputeHost: "host2"},
					{ComputeHost: "host3"},
					{ComputeHost: "host4"},
					{ComputeHost: "host5"},
					{ComputeHost: "host6"},
				},
			},
			expectedHosts: []string{"host1", "host2", "host4", "host5", "host6"}, // All except host3
			filteredHosts: []string{"host3"},                                     // host3 has CUSTOM_STORAGE_SSD
		},
		{
			name: "Invalid trait value - ignored",
			request: api.ExternalSchedulerRequest{
				Spec: api.NovaObject[api.NovaSpec]{
					Data: api.NovaSpec{
						Flavor: api.NovaObject[api.NovaFlavor]{
							Data: api.NovaFlavor{
								ExtraSpecs: map[string]string{
									"trait:COMPUTE_ACCELERATORS":   "invalid_value",
									"trait:COMPUTE_STATUS_ENABLED": "required",
								},
							},
						},
					},
				},
				Hosts: []api.ExternalSchedulerHost{
					{ComputeHost: "host1"},
					{ComputeHost: "host2"},
					{ComputeHost: "host3"},
					{ComputeHost: "host4"},
					{ComputeHost: "host5"},
					{ComputeHost: "host6"},
				},
			},
			expectedHosts: []string{"host2", "host3"}, // Only hosts with COMPUTE_STATUS_ENABLED (invalid value ignored)
			filteredHosts: []string{"host1", "host4", "host5", "host6"},
		},
		{
			name: "Non-trait extra specs - ignored",
			request: api.ExternalSchedulerRequest{
				Spec: api.NovaObject[api.NovaSpec]{
					Data: api.NovaSpec{
						Flavor: api.NovaObject[api.NovaFlavor]{
							Data: api.NovaFlavor{
								ExtraSpecs: map[string]string{
									"hw:cpu_policy":                "dedicated",
									"accel:device_profile":         "gpu-profile",
									"trait:COMPUTE_ACCELERATORS":   "required",
									"capabilities:hypervisor_type": "QEMU",
								},
							},
						},
					},
				},
				Hosts: []api.ExternalSchedulerHost{
					{ComputeHost: "host1"},
					{ComputeHost: "host2"},
					{ComputeHost: "host3"},
					{ComputeHost: "host4"},
					{ComputeHost: "host5"},
					{ComputeHost: "host6"},
				},
			},
			expectedHosts: []string{"host1", "host3", "host6"}, // Only trait: prefixed specs are processed
			filteredHosts: []string{"host2", "host4", "host5"},
		},
		{
			name: "Host with empty traits",
			request: api.ExternalSchedulerRequest{
				Spec: api.NovaObject[api.NovaSpec]{
					Data: api.NovaSpec{
						Flavor: api.NovaObject[api.NovaFlavor]{
							Data: api.NovaFlavor{
								ExtraSpecs: map[string]string{
									"trait:COMPUTE_STATUS_ENABLED": "required",
								},
							},
						},
					},
				},
				Hosts: []api.ExternalSchedulerHost{
					{ComputeHost: "host5"}, // host5 has empty traits
				},
			},
			expectedHosts: []string{},
			filteredHosts: []string{"host5"},
		},
		{
			name: "Host with empty traits - forbidden trait",
			request: api.ExternalSchedulerRequest{
				Spec: api.NovaObject[api.NovaSpec]{
					Data: api.NovaSpec{
						Flavor: api.NovaObject[api.NovaFlavor]{
							Data: api.NovaFlavor{
								ExtraSpecs: map[string]string{
									"trait:COMPUTE_ACCELERATORS": "forbidden",
								},
							},
						},
					},
				},
				Hosts: []api.ExternalSchedulerHost{
					{ComputeHost: "host5"}, // host5 has empty traits
				},
			},
			expectedHosts: []string{"host5"}, // Empty traits means no forbidden traits present
			filteredHosts: []string{},
		},
		{
			name: "No matching hosts",
			request: api.ExternalSchedulerRequest{
				Spec: api.NovaObject[api.NovaSpec]{
					Data: api.NovaSpec{
						Flavor: api.NovaObject[api.NovaFlavor]{
							Data: api.NovaFlavor{
								ExtraSpecs: map[string]string{
									"trait:NONEXISTENT_TRAIT": "required",
								},
							},
						},
					},
				},
				Hosts: []api.ExternalSchedulerHost{
					{ComputeHost: "host1"},
					{ComputeHost: "host2"},
					{ComputeHost: "host3"},
					{ComputeHost: "host4"},
					{ComputeHost: "host5"},
					{ComputeHost: "host6"},
				},
			},
			expectedHosts: []string{},
			filteredHosts: []string{"host1", "host2", "host3", "host4", "host5", "host6"},
		},
		{
			name: "Host not in database",
			request: api.ExternalSchedulerRequest{
				Spec: api.NovaObject[api.NovaSpec]{
					Data: api.NovaSpec{
						Flavor: api.NovaObject[api.NovaFlavor]{
							Data: api.NovaFlavor{
								ExtraSpecs: map[string]string{
									"trait:COMPUTE_ACCELERATORS": "required",
								},
							},
						},
					},
				},
				Hosts: []api.ExternalSchedulerHost{
					{ComputeHost: "host1"},
					{ComputeHost: "host-unknown"},
				},
			},
			expectedHosts: []string{"host1"},
			filteredHosts: []string{"host-unknown"}, // Host not in database gets filtered out
		},
		{
			name: "Empty host list",
			request: api.ExternalSchedulerRequest{
				Spec: api.NovaObject[api.NovaSpec]{
					Data: api.NovaSpec{
						Flavor: api.NovaObject[api.NovaFlavor]{
							Data: api.NovaFlavor{
								ExtraSpecs: map[string]string{
									"trait:COMPUTE_ACCELERATORS": "required",
								},
							},
						},
					},
				},
				Hosts: []api.ExternalSchedulerHost{},
			},
			expectedHosts: []string{},
			filteredHosts: []string{},
		},
		{
			name: "Complex scenario with multiple requirements and restrictions",
			request: api.ExternalSchedulerRequest{
				Spec: api.NovaObject[api.NovaSpec]{
					Data: api.NovaSpec{
						Flavor: api.NovaObject[api.NovaFlavor]{
							Data: api.NovaFlavor{
								ExtraSpecs: map[string]string{
									"trait:COMPUTE_ACCELERATORS":      "required",
									"trait:CUSTOM_GPU_AMD":            "forbidden",
									"trait:COMPUTE_NET_VIRTIO_PACKED": "forbidden",
									"hw:cpu_policy":                   "dedicated", // Should be ignored
								},
							},
						},
					},
				},
				Hosts: []api.ExternalSchedulerHost{
					{ComputeHost: "host1"},
					{ComputeHost: "host2"},
					{ComputeHost: "host3"},
					{ComputeHost: "host4"},
					{ComputeHost: "host5"},
					{ComputeHost: "host6"},
				},
			},
			expectedHosts: []string{"host3"}, // Only host3 has COMPUTE_ACCELERATORS but not the forbidden traits
			filteredHosts: []string{"host1", "host2", "host4", "host5", "host6"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			step := &FilterHasRequestedTraits{}
			if err := step.Init("", testDB, conf.NewRawOpts("{}")); err != nil {
				t.Fatalf("expected no error, got %v", err)
			}
			result, err := step.Run(slog.Default(), tt.request)
			if err != nil {
				t.Fatalf("expected no error, got %v", err)
			}

			// Check expected hosts are present
			for _, host := range tt.expectedHosts {
				if _, ok := result.Activations[host]; !ok {
					t.Errorf("expected host %s to be present in activations", host)
				}
			}

			// Check filtered hosts are not present
			for _, host := range tt.filteredHosts {
				if _, ok := result.Activations[host]; ok {
					t.Errorf("expected host %s to be filtered out", host)
				}
			}

			// Check total count
			if len(result.Activations) != len(tt.expectedHosts) {
				t.Errorf("expected %d hosts, got %d", len(tt.expectedHosts), len(result.Activations))
			}
		})
	}
}

func TestFilterHasRequestedTraits_TraitParsing(t *testing.T) {
	// Set log level debug
	slog.SetLogLoggerLevel(slog.LevelDebug)

	// Test the trait parsing logic with edge cases
	dbEnv := testlibDB.SetupDBEnv(t)
	testDB := db.DB{DbMap: dbEnv.DbMap}
	defer testDB.Close()
	defer dbEnv.Close()

	// Create dependency tables
	err := testDB.CreateTable(
		testDB.AddTable(shared.HostCapabilities{}),
	)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	// Insert test data with edge cases in trait names
	_, err = testDB.Exec(`
		INSERT INTO feature_host_capabilities (compute_host, traits, hypervisor_type)
		VALUES
			('host1', 'TRAIT_WITH_UNDERSCORES,TRAIT-WITH-DASHES,TRAIT.WITH.DOTS', 'QEMU'),
			('host2', 'VERY_LONG_TRAIT_NAME_WITH_MANY_CHARACTERS_AND_NUMBERS_123', 'QEMU'),
			('host3', 'SHORT,A,B,C', 'QEMU')
	`)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	tests := []struct {
		name          string
		extraSpecs    map[string]string
		expectedHosts []string
		filteredHosts []string
	}{
		{
			name: "Trait with underscores",
			extraSpecs: map[string]string{
				"trait:TRAIT_WITH_UNDERSCORES": "required",
			},
			expectedHosts: []string{"host1"},
			filteredHosts: []string{"host2", "host3"},
		},
		{
			name: "Trait with dashes",
			extraSpecs: map[string]string{
				"trait:TRAIT-WITH-DASHES": "required",
			},
			expectedHosts: []string{"host1"},
			filteredHosts: []string{"host2", "host3"},
		},
		{
			name: "Trait with dots",
			extraSpecs: map[string]string{
				"trait:TRAIT.WITH.DOTS": "required",
			},
			expectedHosts: []string{"host1"},
			filteredHosts: []string{"host2", "host3"},
		},
		{
			name: "Very long trait name",
			extraSpecs: map[string]string{
				"trait:VERY_LONG_TRAIT_NAME_WITH_MANY_CHARACTERS_AND_NUMBERS_123": "required",
			},
			expectedHosts: []string{"host2"},
			filteredHosts: []string{"host1", "host3"},
		},
		{
			name: "Short trait names",
			extraSpecs: map[string]string{
				"trait:A": "required",
				"trait:B": "required",
			},
			expectedHosts: []string{"host2", "host3"}, // host2's long trait contains both "A" and "B", host3 has both traits
			filteredHosts: []string{"host1"},          // host1 doesn't have "A" or "B" in its traits
		},
		{
			name: "Case sensitivity test",
			extraSpecs: map[string]string{
				"trait:short": "required", // lowercase, should not match "SHORT"
			},
			expectedHosts: []string{},
			filteredHosts: []string{"host1", "host2", "host3"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			request := api.ExternalSchedulerRequest{
				Spec: api.NovaObject[api.NovaSpec]{
					Data: api.NovaSpec{
						Flavor: api.NovaObject[api.NovaFlavor]{
							Data: api.NovaFlavor{
								ExtraSpecs: tt.extraSpecs,
							},
						},
					},
				},
				Hosts: []api.ExternalSchedulerHost{
					{ComputeHost: "host1"},
					{ComputeHost: "host2"},
					{ComputeHost: "host3"},
				},
			}

			step := &FilterHasRequestedTraits{}
			if err := step.Init("", testDB, conf.NewRawOpts("{}")); err != nil {
				t.Fatalf("expected no error, got %v", err)
			}
			result, err := step.Run(slog.Default(), request)
			if err != nil {
				t.Fatalf("expected no error, got %v", err)
			}

			// Check expected hosts are present
			for _, host := range tt.expectedHosts {
				if _, ok := result.Activations[host]; !ok {
					t.Errorf("expected host %s to be present in activations", host)
				}
			}

			// Check filtered hosts are not present
			for _, host := range tt.filteredHosts {
				if _, ok := result.Activations[host]; ok {
					t.Errorf("expected host %s to be filtered out", host)
				}
			}

			// Check total count
			if len(result.Activations) != len(tt.expectedHosts) {
				t.Errorf("expected %d hosts, got %d", len(tt.expectedHosts), len(result.Activations))
			}
		})
	}
}
