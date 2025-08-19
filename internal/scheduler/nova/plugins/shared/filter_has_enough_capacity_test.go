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

func TestFilterHasEnoughCapacity_Run(t *testing.T) {
	dbEnv := testlibDB.SetupDBEnv(t)
	testDB := db.DB{DbMap: dbEnv.DbMap}
	defer testDB.Close()
	defer dbEnv.Close()

	// Create dependency tables
	err := testDB.CreateTable(
		testDB.AddTable(shared.HostUtilization{}),
	)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	// Insert mock data into the feature_host_utilization table
	hostUtilizations := []any{
		&shared.HostUtilization{ComputeHost: "host1", RAMUtilizedPct: 50.0, VCPUsUtilizedPct: 40.0, DiskUtilizedPct: 30.0, TotalMemoryAllocatableMB: 32768, TotalVCPUsAllocatable: 16, TotalDiskAllocatableGB: 1000}, // High capacity host
		&shared.HostUtilization{ComputeHost: "host2", RAMUtilizedPct: 80.0, VCPUsUtilizedPct: 70.0, DiskUtilizedPct: 60.0, TotalMemoryAllocatableMB: 16384, TotalVCPUsAllocatable: 8, TotalDiskAllocatableGB: 500},   // Medium capacity host
		&shared.HostUtilization{ComputeHost: "host3", RAMUtilizedPct: 90.0, VCPUsUtilizedPct: 85.0, DiskUtilizedPct: 75.0, TotalMemoryAllocatableMB: 8192, TotalVCPUsAllocatable: 4, TotalDiskAllocatableGB: 250},    // Low capacity host
		&shared.HostUtilization{ComputeHost: "host4", RAMUtilizedPct: 20.0, VCPUsUtilizedPct: 15.0, DiskUtilizedPct: 10.0, TotalMemoryAllocatableMB: 65536, TotalVCPUsAllocatable: 32, TotalDiskAllocatableGB: 2000}, // Very high capacity host
		&shared.HostUtilization{ComputeHost: "host5", RAMUtilizedPct: 95.0, VCPUsUtilizedPct: 90.0, DiskUtilizedPct: 85.0, TotalMemoryAllocatableMB: 4096, TotalVCPUsAllocatable: 2, TotalDiskAllocatableGB: 100},    // Very low capacity host
		&shared.HostUtilization{ComputeHost: "host6", RAMUtilizedPct: 0.0, VCPUsUtilizedPct: 0.0, DiskUtilizedPct: 0.0, TotalMemoryAllocatableMB: 0, TotalVCPUsAllocatable: 0, TotalDiskAllocatableGB: 0},            // Zero capacity host (edge case)
	}
	if err := testDB.Insert(hostUtilizations...); err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	tests := []struct {
		name          string
		request       api.ExternalSchedulerRequest
		expectedHosts []string
		filteredHosts []string
	}{
		{
			name: "Small flavor - most hosts have capacity",
			request: api.ExternalSchedulerRequest{
				Spec: api.NovaObject[api.NovaSpec]{
					Data: api.NovaSpec{
						Flavor: api.NovaObject[api.NovaFlavor]{
							Data: api.NovaFlavor{
								VCPUs:    2,
								MemoryMB: 4096,
								RootGB:   50,
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
			expectedHosts: []string{"host1", "host2", "host3", "host4", "host5"}, // All except host6 (0 capacity) - host5 has exactly 2 vCPUs
			filteredHosts: []string{"host6"},
		},
		{
			name: "Medium flavor - some hosts filtered",
			request: api.ExternalSchedulerRequest{
				Spec: api.NovaObject[api.NovaSpec]{
					Data: api.NovaSpec{
						Flavor: api.NovaObject[api.NovaFlavor]{
							Data: api.NovaFlavor{
								VCPUs:    8,
								MemoryMB: 16384,
								RootGB:   200,
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
			expectedHosts: []string{"host1", "host2", "host4"}, // Only hosts with >= 8 vCPUs, >= 16384 MB RAM, >= 200 GB disk
			filteredHosts: []string{"host3", "host5", "host6"}, // host3 has only 4 vCPUs, host5 has only 2 vCPUs, host6 has 0
		},
		{
			name: "Large flavor - only high capacity hosts",
			request: api.ExternalSchedulerRequest{
				Spec: api.NovaObject[api.NovaSpec]{
					Data: api.NovaSpec{
						Flavor: api.NovaObject[api.NovaFlavor]{
							Data: api.NovaFlavor{
								VCPUs:    16,
								MemoryMB: 32768,
								RootGB:   500,
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
			expectedHosts: []string{"host1", "host4"}, // Only hosts with >= 16 vCPUs, >= 32768 MB RAM, >= 500 GB disk
			filteredHosts: []string{"host2", "host3", "host5", "host6"},
		},
		{
			name: "Very large flavor - only very high capacity host",
			request: api.ExternalSchedulerRequest{
				Spec: api.NovaObject[api.NovaSpec]{
					Data: api.NovaSpec{
						Flavor: api.NovaObject[api.NovaFlavor]{
							Data: api.NovaFlavor{
								VCPUs:    32,
								MemoryMB: 65536,
								RootGB:   1000,
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
			expectedHosts: []string{"host4"}, // Only host4 has enough capacity
			filteredHosts: []string{"host1", "host2", "host3", "host5", "host6"},
		},
		{
			name: "Impossible flavor - no hosts have capacity",
			request: api.ExternalSchedulerRequest{
				Spec: api.NovaObject[api.NovaSpec]{
					Data: api.NovaSpec{
						Flavor: api.NovaObject[api.NovaFlavor]{
							Data: api.NovaFlavor{
								VCPUs:    64,
								MemoryMB: 131072,
								RootGB:   5000,
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
			expectedHosts: []string{}, // No hosts have enough capacity
			filteredHosts: []string{"host1", "host2", "host3", "host4", "host5", "host6"},
		},
		{
			name: "CPU constraint only",
			request: api.ExternalSchedulerRequest{
				Spec: api.NovaObject[api.NovaSpec]{
					Data: api.NovaSpec{
						Flavor: api.NovaObject[api.NovaFlavor]{
							Data: api.NovaFlavor{
								VCPUs:    10, // More than host3 (4) and host5 (2)
								MemoryMB: 1024,
								RootGB:   10,
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
			expectedHosts: []string{"host1", "host4"}, // Only hosts with >= 10 vCPUs
			filteredHosts: []string{"host2", "host3", "host5", "host6"},
		},
		{
			name: "Memory constraint only",
			request: api.ExternalSchedulerRequest{
				Spec: api.NovaObject[api.NovaSpec]{
					Data: api.NovaSpec{
						Flavor: api.NovaObject[api.NovaFlavor]{
							Data: api.NovaFlavor{
								VCPUs:    1,
								MemoryMB: 20000, // More than host3 (8192) and host5 (4096)
								RootGB:   10,
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
			expectedHosts: []string{"host1", "host4"}, // Only hosts with >= 20000 MB RAM
			filteredHosts: []string{"host2", "host3", "host5", "host6"},
		},
		{
			name: "Disk constraint only",
			request: api.ExternalSchedulerRequest{
				Spec: api.NovaObject[api.NovaSpec]{
					Data: api.NovaSpec{
						Flavor: api.NovaObject[api.NovaFlavor]{
							Data: api.NovaFlavor{
								VCPUs:    1,
								MemoryMB: 1024,
								RootGB:   600, // More than host3 (250) and host5 (100)
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
			expectedHosts: []string{"host1", "host4"}, // Only hosts with >= 600 GB disk
			filteredHosts: []string{"host2", "host3", "host5", "host6"},
		},
		{
			name: "Zero resource flavor",
			request: api.ExternalSchedulerRequest{
				Spec: api.NovaObject[api.NovaSpec]{
					Data: api.NovaSpec{
						Flavor: api.NovaObject[api.NovaFlavor]{
							Data: api.NovaFlavor{
								VCPUs:    0,
								MemoryMB: 0,
								RootGB:   0,
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
			expectedHosts: []string{"host1", "host2", "host3", "host4", "host5", "host6"}, // All hosts can handle zero resources
			filteredHosts: []string{},
		},
		{
			name: "Host not in database",
			request: api.ExternalSchedulerRequest{
				Spec: api.NovaObject[api.NovaSpec]{
					Data: api.NovaSpec{
						Flavor: api.NovaObject[api.NovaFlavor]{
							Data: api.NovaFlavor{
								VCPUs:    2,
								MemoryMB: 4096,
								RootGB:   50,
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
								VCPUs:    2,
								MemoryMB: 4096,
								RootGB:   50,
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
			name: "Exact capacity match",
			request: api.ExternalSchedulerRequest{
				Spec: api.NovaObject[api.NovaSpec]{
					Data: api.NovaSpec{
						Flavor: api.NovaObject[api.NovaFlavor]{
							Data: api.NovaFlavor{
								VCPUs:    8, // Exactly matches host2
								MemoryMB: 16384,
								RootGB:   500,
							},
						},
					},
				},
				Hosts: []api.ExternalSchedulerHost{
					{ComputeHost: "host1"},
					{ComputeHost: "host2"},
					{ComputeHost: "host3"},
					{ComputeHost: "host4"},
				},
			},
			expectedHosts: []string{"host1", "host2", "host4"}, // host2 exactly matches, host1 and host4 exceed
			filteredHosts: []string{"host3"},                   // host3 has insufficient capacity
		},
		{
			name: "Boundary test - just over capacity",
			request: api.ExternalSchedulerRequest{
				Spec: api.NovaObject[api.NovaSpec]{
					Data: api.NovaSpec{
						Flavor: api.NovaObject[api.NovaFlavor]{
							Data: api.NovaFlavor{
								VCPUs:    9,     // Just over host2's 8 vCPUs
								MemoryMB: 16385, // Just over host2's 16384 MB
								RootGB:   501,   // Just over host2's 500 GB
							},
						},
					},
				},
				Hosts: []api.ExternalSchedulerHost{
					{ComputeHost: "host1"},
					{ComputeHost: "host2"},
					{ComputeHost: "host3"},
					{ComputeHost: "host4"},
				},
			},
			expectedHosts: []string{"host1", "host4"}, // Only hosts that exceed the requirements
			filteredHosts: []string{"host2", "host3"}, // host2 is just under, host3 is well under
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			step := &FilterHasEnoughCapacity{}
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

func TestFilterHasEnoughCapacity_EdgeCases(t *testing.T) {
	dbEnv := testlibDB.SetupDBEnv(t)
	testDB := db.DB{DbMap: dbEnv.DbMap}
	defer testDB.Close()
	defer dbEnv.Close()

	// Create dependency tables
	err := testDB.CreateTable(
		testDB.AddTable(shared.HostUtilization{}),
	)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	// Insert edge case data
	hostUtilizationsEdgeCases := []any{
		&shared.HostUtilization{ComputeHost: "host1", RAMUtilizedPct: 50.0, VCPUsUtilizedPct: 40.0, DiskUtilizedPct: 30.0, TotalMemoryAllocatableMB: 1.5, TotalVCPUsAllocatable: 0.5, TotalDiskAllocatableGB: 0.5},     // Fractional capacity
		&shared.HostUtilization{ComputeHost: "host2", RAMUtilizedPct: 0.0, VCPUsUtilizedPct: 0.0, DiskUtilizedPct: 0.0, TotalMemoryAllocatableMB: 1000000, TotalVCPUsAllocatable: 1000, TotalDiskAllocatableGB: 10000}, // Very large capacity
		&shared.HostUtilization{ComputeHost: "host3", RAMUtilizedPct: 100.0, VCPUsUtilizedPct: 100.0, DiskUtilizedPct: 100.0, TotalMemoryAllocatableMB: -100, TotalVCPUsAllocatable: -10, TotalDiskAllocatableGB: -50}, // Negative capacity (edge case)
	}
	if err := testDB.Insert(hostUtilizationsEdgeCases...); err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	tests := []struct {
		name          string
		flavor        api.NovaFlavor
		expectedHosts []string
		filteredHosts []string
	}{
		{
			name: "Fractional capacity vs integer requirements",
			flavor: api.NovaFlavor{
				VCPUs:    1,
				MemoryMB: 1,
				RootGB:   1,
			},
			expectedHosts: []string{"host2"},          // Only host2 has enough capacity
			filteredHosts: []string{"host1", "host3"}, // host1 has fractional capacity < 1, host3 has negative
		},
		{
			name: "Very large flavor vs very large capacity",
			flavor: api.NovaFlavor{
				VCPUs:    500,
				MemoryMB: 500000,
				RootGB:   5000,
			},
			expectedHosts: []string{"host2"}, // Only host2 has very large capacity
			filteredHosts: []string{"host1", "host3"},
		},
		{
			name: "Zero requirements",
			flavor: api.NovaFlavor{
				VCPUs:    0,
				MemoryMB: 0,
				RootGB:   0,
			},
			expectedHosts: []string{"host1", "host2"}, // host3 with negative capacity gets filtered out
			filteredHosts: []string{"host3"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			request := api.ExternalSchedulerRequest{
				Spec: api.NovaObject[api.NovaSpec]{
					Data: api.NovaSpec{
						Flavor: api.NovaObject[api.NovaFlavor]{
							Data: tt.flavor,
						},
					},
				},
				Hosts: []api.ExternalSchedulerHost{
					{ComputeHost: "host1"},
					{ComputeHost: "host2"},
					{ComputeHost: "host3"},
				},
			}

			step := &FilterHasEnoughCapacity{}
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

func TestFilterHasEnoughCapacity_ResourceTypes(t *testing.T) {
	dbEnv := testlibDB.SetupDBEnv(t)
	testDB := db.DB{DbMap: dbEnv.DbMap}
	defer testDB.Close()
	defer dbEnv.Close()

	// Create dependency tables
	err := testDB.CreateTable(
		testDB.AddTable(shared.HostUtilization{}),
	)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	// Insert specialized capacity data for individual resource testing
	hostUtilizationsResourceTypes := []any{
		&shared.HostUtilization{ComputeHost: "cpu-rich", RAMUtilizedPct: 50.0, VCPUsUtilizedPct: 40.0, DiskUtilizedPct: 30.0, TotalMemoryAllocatableMB: 8192, TotalVCPUsAllocatable: 64, TotalDiskAllocatableGB: 500},   // High CPU, medium RAM/disk
		&shared.HostUtilization{ComputeHost: "ram-rich", RAMUtilizedPct: 50.0, VCPUsUtilizedPct: 40.0, DiskUtilizedPct: 30.0, TotalMemoryAllocatableMB: 131072, TotalVCPUsAllocatable: 8, TotalDiskAllocatableGB: 500},  // High RAM, medium CPU/disk
		&shared.HostUtilization{ComputeHost: "disk-rich", RAMUtilizedPct: 50.0, VCPUsUtilizedPct: 40.0, DiskUtilizedPct: 30.0, TotalMemoryAllocatableMB: 8192, TotalVCPUsAllocatable: 8, TotalDiskAllocatableGB: 10000}, // High disk, medium CPU/RAM
		&shared.HostUtilization{ComputeHost: "balanced", RAMUtilizedPct: 50.0, VCPUsUtilizedPct: 40.0, DiskUtilizedPct: 30.0, TotalMemoryAllocatableMB: 16384, TotalVCPUsAllocatable: 16, TotalDiskAllocatableGB: 1000}, // Balanced resources
	}
	if err := testDB.Insert(hostUtilizationsResourceTypes...); err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	tests := []struct {
		name          string
		flavor        api.NovaFlavor
		expectedHosts []string
		description   string
	}{
		{
			name: "CPU-intensive flavor",
			flavor: api.NovaFlavor{
				VCPUs:    32,
				MemoryMB: 4096,
				RootGB:   100,
			},
			expectedHosts: []string{"cpu-rich"}, // Only cpu-rich has 64 vCPUs, balanced has only 16
			description:   "Should pass hosts with sufficient CPU",
		},
		{
			name: "RAM-intensive flavor",
			flavor: api.NovaFlavor{
				VCPUs:    4,
				MemoryMB: 65536,
				RootGB:   100,
			},
			expectedHosts: []string{"ram-rich"}, // Only ram-rich has 131072 MB, balanced has only 16384 MB
			description:   "Should pass hosts with sufficient RAM",
		},
		{
			name: "Disk-intensive flavor",
			flavor: api.NovaFlavor{
				VCPUs:    4,
				MemoryMB: 4096,
				RootGB:   5000,
			},
			expectedHosts: []string{"disk-rich"},
			description:   "Should pass hosts with sufficient disk",
		},
		{
			name: "Multi-resource intensive flavor",
			flavor: api.NovaFlavor{
				VCPUs:    16,
				MemoryMB: 16384,
				RootGB:   1000,
			},
			expectedHosts: []string{"balanced"},
			description:   "Should pass only balanced host with all resources",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			request := api.ExternalSchedulerRequest{
				Spec: api.NovaObject[api.NovaSpec]{
					Data: api.NovaSpec{
						Flavor: api.NovaObject[api.NovaFlavor]{
							Data: tt.flavor,
						},
					},
				},
				Hosts: []api.ExternalSchedulerHost{
					{ComputeHost: "cpu-rich"},
					{ComputeHost: "ram-rich"},
					{ComputeHost: "disk-rich"},
					{ComputeHost: "balanced"},
				},
			}

			step := &FilterHasEnoughCapacity{}
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
					t.Errorf("expected host %s to be present in activations for %s", host, tt.description)
				}
			}

			// Check total count
			if len(result.Activations) != len(tt.expectedHosts) {
				t.Errorf("expected %d hosts, got %d for %s", len(tt.expectedHosts), len(result.Activations), tt.description)
			}
		})
	}
}
