// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package shared

import (
	"reflect"
	"testing"

	"github.com/cobaltcore-dev/cortex/knowledge/api/datasources/openstack/nova"
	"github.com/cobaltcore-dev/cortex/knowledge/api/datasources/openstack/placement"
	"github.com/cobaltcore-dev/cortex/knowledge/api/features/shared"
	"github.com/cobaltcore-dev/cortex/knowledge/api/v1alpha1"
	"github.com/cobaltcore-dev/cortex/lib/db"
	testlibDB "github.com/cobaltcore-dev/cortex/testlib/db"
)

func TestHostUtilizationExtractor_Init(t *testing.T) {
	dbEnv := testlibDB.SetupDBEnv(t)
	testDB := db.DB{DbMap: dbEnv.DbMap}
	defer testDB.Close()
	defer dbEnv.Close()

	extractor := &HostUtilizationExtractor{}
	config := v1alpha1.KnowledgeSpec{}
	if err := extractor.Init(&testDB, config); err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	if !testDB.TableExists(shared.HostUtilization{}) {
		t.Error("expected table to be created")
	}
}

func TestHostUtilizationExtractor_Extract(t *testing.T) {
	tests := []struct {
		name     string
		mockData []any
		expected []shared.HostUtilization
	}{
		{
			name:     "should return empty list when no hypervisors exist",
			mockData: []any{
				// No hypervisors
			},
			expected: []shared.HostUtilization{},
		},
		{
			name: "should return correct host utilization with no reserved capacity",
			mockData: []any{
				// Hypervisors
				&nova.Hypervisor{ID: "1", Hostname: "hostname1", ServiceHost: "host1"},
				// No usage data for this host
				&nova.Hypervisor{ID: "2", Hostname: "hostname2", ServiceHost: "host2"},

				// Placement inventory usage
				&placement.InventoryUsage{
					ResourceProviderUUID: "1",
					InventoryClassName:   "VCPU",
					AllocationRatio:      1.0,
					Total:                100,
					Used:                 25,
					Reserved:             0,
				},
				&placement.InventoryUsage{
					ResourceProviderUUID: "1",
					InventoryClassName:   "MEMORY_MB",
					AllocationRatio:      1.0,
					Total:                1000,
					Used:                 500,
					Reserved:             0,
				},
				&placement.InventoryUsage{
					ResourceProviderUUID: "1",
					InventoryClassName:   "DISK_GB",
					AllocationRatio:      1.0,
					Total:                2000,
					Used:                 1000,
					Reserved:             0,
				},
			},
			expected: []shared.HostUtilization{
				{
					ComputeHost:            "host1",
					TotalVCPUsAllocatable:  100,
					TotalRAMAllocatableMB:  1000,
					TotalDiskAllocatableGB: 2000,
					VCPUsUsed:              25,
					RAMUsedMB:              500,
					DiskUsedGB:             1000,
					VCPUsUtilizedPct:       25,
					RAMUtilizedPct:         50,
					DiskUtilizedPct:        50,
				},
			},
		},
		{
			name: "should return correct host utilization with reserved capacity",
			mockData: []any{
				// Hypervisors
				&nova.Hypervisor{ID: "1", Hostname: "hostname1", ServiceHost: "host1"},
				// No usage data for this host
				&nova.Hypervisor{ID: "2", Hostname: "hostname2", ServiceHost: "host2"},

				// Placement inventory usage
				&placement.InventoryUsage{
					ResourceProviderUUID: "1",
					InventoryClassName:   "VCPU",
					AllocationRatio:      1.0,
					Total:                105,
					Used:                 25,
					Reserved:             5,
				},
				&placement.InventoryUsage{
					ResourceProviderUUID: "1",
					InventoryClassName:   "MEMORY_MB",
					AllocationRatio:      1.0,
					Total:                1100,
					Used:                 500,
					Reserved:             100,
				},
				&placement.InventoryUsage{
					ResourceProviderUUID: "1",
					InventoryClassName:   "DISK_GB",
					AllocationRatio:      1.0,
					Total:                2100,
					Used:                 1000,
					Reserved:             100,
				},
			},
			expected: []shared.HostUtilization{
				{
					ComputeHost:            "host1",
					TotalVCPUsAllocatable:  100,
					TotalRAMAllocatableMB:  1000,
					TotalDiskAllocatableGB: 2000,
					VCPUsUsed:              25,
					RAMUsedMB:              500,
					DiskUsedGB:             1000,
					VCPUsUtilizedPct:       25,
					RAMUtilizedPct:         50,
					DiskUtilizedPct:        50,
				},
			},
		},
		{
			name: "should return correct host utilization with overcommit factor",
			mockData: []any{
				// Hypervisors
				&nova.Hypervisor{ID: "1", Hostname: "hostname1", ServiceHost: "host1"},
				// No usage data for this host
				&nova.Hypervisor{ID: "2", Hostname: "hostname2", ServiceHost: "host2"},

				// Placement inventory usage
				// Expect total allocatable vcpus to be 200 [(110-10)*2]
				&placement.InventoryUsage{
					ResourceProviderUUID: "1",
					InventoryClassName:   "VCPU",
					AllocationRatio:      2.0,
					Total:                110, // Does not include overcommit
					Used:                 50,  // Already includes overcommit
					Reserved:             10,  // Does not include overcommit
				},
				&placement.InventoryUsage{
					ResourceProviderUUID: "1",
					InventoryClassName:   "MEMORY_MB",
					AllocationRatio:      1.0,
					Total:                1000,
					Used:                 500,
					Reserved:             0,
				},
				&placement.InventoryUsage{
					ResourceProviderUUID: "1",
					InventoryClassName:   "DISK_GB",
					AllocationRatio:      1.0,
					Total:                2000,
					Used:                 1000,
					Reserved:             0,
				},
			},
			expected: []shared.HostUtilization{
				{
					ComputeHost:            "host1",
					TotalVCPUsAllocatable:  200,
					TotalRAMAllocatableMB:  1000,
					TotalDiskAllocatableGB: 2000,
					VCPUsUsed:              50,
					RAMUsedMB:              500,
					DiskUsedGB:             1000,
					VCPUsUtilizedPct:       25,
					RAMUtilizedPct:         50,
					DiskUtilizedPct:        50,
				},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dbEnv := testlibDB.SetupDBEnv(t)
			testDB := db.DB{DbMap: dbEnv.DbMap}
			defer testDB.Close()
			defer dbEnv.Close()

			if err := testDB.CreateTable(
				testDB.AddTable(placement.InventoryUsage{}),
				testDB.AddTable(nova.Hypervisor{}),
			); err != nil {
				t.Fatalf("expected no error, got %v", err)
			}
			if err := testDB.Insert(tt.mockData...); err != nil {
				t.Fatalf("expected no error, got %v", err)
			}

			extractor := &HostUtilizationExtractor{}
			config := v1alpha1.KnowledgeSpec{}

			if err := extractor.Init(&testDB, config); err != nil {
				t.Fatalf("expected no error, got %v", err)
			}

			if _, err := extractor.Extract(); err != nil {
				t.Fatalf("expected no error, got %v", err)
			}

			var hostUtilizations []shared.HostUtilization
			table := shared.HostUtilization{}.TableName()
			if _, err := testDB.Select(&hostUtilizations, "SELECT * FROM "+table); err != nil {
				t.Fatalf("expected no error, got %v", err)
			}

			// Check if the expected hosts match the extracted ones
			if len(hostUtilizations) != len(tt.expected) {
				t.Fatalf("expected %d host utilizations, got %d", len(tt.expected), len(hostUtilizations))
			}
			// Compare each expected host with the extracted ones
			if !reflect.DeepEqual(tt.expected, hostUtilizations) {
				t.Errorf("expected %v, got %v", tt.expected, hostUtilizations)
			}
		})
	}
}
