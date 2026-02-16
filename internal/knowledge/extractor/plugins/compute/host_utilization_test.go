// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package compute

import (
	"reflect"
	"testing"

	"github.com/cobaltcore-dev/cortex/api/v1alpha1"
	"github.com/cobaltcore-dev/cortex/internal/knowledge/datasources/plugins/openstack/nova"
	"github.com/cobaltcore-dev/cortex/internal/knowledge/datasources/plugins/openstack/placement"
	"github.com/cobaltcore-dev/cortex/internal/knowledge/db"
	testlibDB "github.com/cobaltcore-dev/cortex/internal/knowledge/db/testing"
)

func TestHostUtilizationExtractor_Init(t *testing.T) {
	extractor := &HostUtilizationExtractor{}
	config := v1alpha1.KnowledgeSpec{}
	if err := extractor.Init(nil, nil, config); err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
}

func TestHostUtilizationExtractor_Extract(t *testing.T) {
	tests := []struct {
		name     string
		mockData []any
		expected []HostUtilization
	}{
		{
			name:     "should return empty list when no hypervisors exist",
			mockData: []any{
				// No hypervisors
			},
			expected: []HostUtilization{},
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
			expected: []HostUtilization{
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
			expected: []HostUtilization{
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
			expected: []HostUtilization{
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

			if err := extractor.Init(&testDB, nil, config); err != nil {
				t.Fatalf("expected no error, got %v", err)
			}

			features, err := extractor.Extract()
			if err != nil {
				t.Fatalf("expected no error, got %v", err)
			}

			// Check if the expected hosts match the extracted ones
			if len(features) != len(tt.expected) {
				t.Fatalf("expected %d host utilizations, got %d", len(tt.expected), len(features))
			}
			// Compare each expected host with the extracted ones
			for i, exp := range tt.expected {
				if !reflect.DeepEqual(exp, features[i]) {
					t.Errorf("expected host utilization %v, got %v", exp, features[i])
				}
			}
		})
	}
}
