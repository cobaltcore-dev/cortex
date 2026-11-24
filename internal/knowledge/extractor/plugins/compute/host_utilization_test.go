// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package compute

import (
	"slices"
	"testing"

	"github.com/cobaltcore-dev/cortex/pkg/knowledge/datasources/openstack/nova"
	"github.com/cobaltcore-dev/cortex/pkg/knowledge/datasources/openstack/placement"
)

func TestHostUtilizationExtractor_Extract(t *testing.T) {
	usage := func(class string, ratio float32, total, used, reserved int) placement.InventoryUsage {
		return placement.InventoryUsage{
			ResourceProviderUUID: "1",
			InventoryClassName:   class,
			AllocationRatio:      ratio,
			Total:                total,
			Used:                 used,
			Reserved:             reserved,
		}
	}
	hypervisors := []nova.Hypervisor{
		{ID: "1", Hostname: "hostname1", ServiceHost: "host1"},
		// No usage data for this host.
		{ID: "2", Hostname: "hostname2", ServiceHost: "host2"},
	}
	expected := HostUtilization{
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
	}
	withOvercommit := expected
	withOvercommit.TotalVCPUsAllocatable = 200
	withOvercommit.VCPUsUsed = 50

	tests := []struct {
		name     string
		in       HostUtilizationInputs
		expected []HostUtilization
	}{
		{
			name:     "no hypervisors",
			expected: nil,
		},
		{
			name: "no reserved capacity",
			in: HostUtilizationInputs{
				Hypervisors: hypervisors,
				InventoryUsages: []placement.InventoryUsage{
					usage("VCPU", 1, 100, 25, 0),
					usage("MEMORY_MB", 1, 1000, 500, 0),
					usage("DISK_GB", 1, 2000, 1000, 0),
				},
			},
			expected: []HostUtilization{expected},
		},
		{
			name: "reserved capacity",
			in: HostUtilizationInputs{
				Hypervisors: hypervisors,
				InventoryUsages: []placement.InventoryUsage{
					usage("VCPU", 1, 105, 25, 5),
					usage("MEMORY_MB", 1, 1100, 500, 100),
					usage("DISK_GB", 1, 2100, 1000, 100),
				},
			},
			expected: []HostUtilization{expected},
		},
		{
			name: "overcommit factor",
			in: HostUtilizationInputs{
				Hypervisors: hypervisors,
				InventoryUsages: []placement.InventoryUsage{
					// Total allocatable vcpus: (110-10)*2 = 200, used already includes overcommit.
					usage("VCPU", 2, 110, 50, 10),
					usage("MEMORY_MB", 1, 1000, 500, 0),
					usage("DISK_GB", 1, 2000, 1000, 0),
				},
			},
			expected: []HostUtilization{withOvercommit},
		},
		{
			name: "incomplete inventory is skipped",
			in: HostUtilizationInputs{
				Hypervisors: hypervisors,
				InventoryUsages: []placement.InventoryUsage{
					usage("VCPU", 1, 100, 25, 0),
					usage("MEMORY_MB", 1, 1000, 500, 0),
				},
			},
			expected: nil,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			features, err := (&HostUtilizationExtractor{}).Extract(t.Context(), tt.in)
			if err != nil {
				t.Fatalf("expected no error, got %v", err)
			}
			if !slices.Equal(features, tt.expected) {
				t.Errorf("expected %+v, got %+v", tt.expected, features)
			}
		})
	}
}
