// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package commitments

import (
	"testing"
	"time"
)

func TestSortVMsForUsageCalculation(t *testing.T) {
	baseTime := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)

	tests := []struct {
		name     string
		input    []VMUsageInfo
		expected []string // Expected order of UUIDs
	}{
		{
			name:     "empty list",
			input:    []VMUsageInfo{},
			expected: []string{},
		},
		{
			name: "sort by creation time - oldest first",
			input: []VMUsageInfo{
				{UUID: "vm-newest", CreatedAt: baseTime.Add(2 * time.Hour), MemoryMB: 1024},
				{UUID: "vm-oldest", CreatedAt: baseTime, MemoryMB: 1024},
				{UUID: "vm-middle", CreatedAt: baseTime.Add(1 * time.Hour), MemoryMB: 1024},
			},
			expected: []string{"vm-oldest", "vm-middle", "vm-newest"},
		},
		{
			name: "same creation time - largest first",
			input: []VMUsageInfo{
				{UUID: "vm-small", CreatedAt: baseTime, MemoryMB: 1024},
				{UUID: "vm-large", CreatedAt: baseTime, MemoryMB: 8192},
				{UUID: "vm-medium", CreatedAt: baseTime, MemoryMB: 4096},
			},
			expected: []string{"vm-large", "vm-medium", "vm-small"},
		},
		{
			name: "same time and size - sort by UUID",
			input: []VMUsageInfo{
				{UUID: "vm-c", CreatedAt: baseTime, MemoryMB: 1024},
				{UUID: "vm-a", CreatedAt: baseTime, MemoryMB: 1024},
				{UUID: "vm-b", CreatedAt: baseTime, MemoryMB: 1024},
			},
			expected: []string{"vm-a", "vm-b", "vm-c"},
		},
		{
			name: "mixed criteria",
			input: []VMUsageInfo{
				{UUID: "vm-new-large", CreatedAt: baseTime.Add(1 * time.Hour), MemoryMB: 8192},
				{UUID: "vm-old-small", CreatedAt: baseTime, MemoryMB: 1024},
				{UUID: "vm-old-large", CreatedAt: baseTime, MemoryMB: 8192},
			},
			expected: []string{"vm-old-large", "vm-old-small", "vm-new-large"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			sortVMsForUsageCalculation(tt.input)

			if len(tt.input) != len(tt.expected) {
				t.Fatalf("Length mismatch: got %d, expected %d", len(tt.input), len(tt.expected))
			}

			for i, expectedUUID := range tt.expected {
				if tt.input[i].UUID != expectedUUID {
					t.Errorf("Position %d: expected %s, got %s", i, expectedUUID, tt.input[i].UUID)
				}
			}
		})
	}
}

func TestSortCommitmentsForAssignment(t *testing.T) {
	baseTime := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	time1 := baseTime
	time2 := baseTime.Add(1 * time.Hour)

	tests := []struct {
		name     string
		input    []*CommitmentStateWithUsage
		expected []string // Expected order of CommitmentUUIDs
	}{
		{
			name:     "empty list",
			input:    []*CommitmentStateWithUsage{},
			expected: []string{},
		},
		{
			name: "sort by start time - oldest first",
			input: []*CommitmentStateWithUsage{
				{CommitmentState: CommitmentState{CommitmentUUID: "commit-new", StartTime: &time2, TotalMemoryBytes: 1024}},
				{CommitmentState: CommitmentState{CommitmentUUID: "commit-old", StartTime: &time1, TotalMemoryBytes: 1024}},
			},
			expected: []string{"commit-old", "commit-new"},
		},
		{
			name: "nil start time treated as oldest",
			input: []*CommitmentStateWithUsage{
				{CommitmentState: CommitmentState{CommitmentUUID: "commit-with-time", StartTime: &time1, TotalMemoryBytes: 1024}},
				{CommitmentState: CommitmentState{CommitmentUUID: "commit-no-time", StartTime: nil, TotalMemoryBytes: 1024}},
			},
			expected: []string{"commit-no-time", "commit-with-time"},
		},
		{
			name: "same start time - largest first",
			input: []*CommitmentStateWithUsage{
				{CommitmentState: CommitmentState{CommitmentUUID: "commit-small", StartTime: &time1, TotalMemoryBytes: 1024}},
				{CommitmentState: CommitmentState{CommitmentUUID: "commit-large", StartTime: &time1, TotalMemoryBytes: 8192}},
			},
			expected: []string{"commit-large", "commit-small"},
		},
		{
			name: "same time and size - sort by UUID",
			input: []*CommitmentStateWithUsage{
				{CommitmentState: CommitmentState{CommitmentUUID: "commit-c", StartTime: &time1, TotalMemoryBytes: 1024}},
				{CommitmentState: CommitmentState{CommitmentUUID: "commit-a", StartTime: &time1, TotalMemoryBytes: 1024}},
				{CommitmentState: CommitmentState{CommitmentUUID: "commit-b", StartTime: &time1, TotalMemoryBytes: 1024}},
			},
			expected: []string{"commit-a", "commit-b", "commit-c"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			sortCommitmentsForAssignment(tt.input)

			if len(tt.input) != len(tt.expected) {
				t.Fatalf("Length mismatch: got %d, expected %d", len(tt.input), len(tt.expected))
			}

			for i, expectedUUID := range tt.expected {
				if tt.input[i].CommitmentUUID != expectedUUID {
					t.Errorf("Position %d: expected %s, got %s", i, expectedUUID, tt.input[i].CommitmentUUID)
				}
			}
		})
	}
}

func TestAzFlavorGroupKey(t *testing.T) {
	tests := []struct {
		az          string
		flavorGroup string
		expected    string
	}{
		{"az-a", "hana_1", "az-a:hana_1"},
		{"", "hana_1", ":hana_1"},
		{"az-a", "", "az-a:"},
		{"", "", ":"},
		{"us-west-1a", "gpu_large_v2", "us-west-1a:gpu_large_v2"},
	}

	for _, tt := range tests {
		t.Run(tt.expected, func(t *testing.T) {
			result := azFlavorGroupKey(tt.az, tt.flavorGroup)
			if result != tt.expected {
				t.Errorf("azFlavorGroupKey(%q, %q) = %q, expected %q",
					tt.az, tt.flavorGroup, result, tt.expected)
			}
		})
	}
}

func TestBuildVMAttributes(t *testing.T) {
	vm := VMUsageInfo{
		UUID:       "vm-123",
		Name:       "my-vm",
		FlavorName: "m1.large",
		Status:     "ACTIVE",
		Hypervisor: "host-1",
		MemoryMB:   4096,
		VCPUs:      16,
		DiskGB:     100,
		OSType:     "windows8Server64Guest",
	}

	t.Run("with commitment", func(t *testing.T) {
		attrs := buildVMAttributes(vm, "commit-456")

		if attrs["status"] != "ACTIVE" {
			t.Errorf("status = %v, expected ACTIVE", attrs["status"])
		}

		if attrs["os_type"] != "windows8Server64Guest" {
			t.Errorf("os_type = %v, expected windows8Server64Guest", attrs["os_type"])
		}

		for _, absent := range []string{"metadata", "tags"} {
			if _, present := attrs[absent]; present {
				t.Errorf("%s must not appear in output (not available from Postgres cache)", absent)
			}
		}

		flavor, ok := attrs["flavor"].(map[string]any)
		if !ok {
			t.Errorf("flavor is not map[string]any: %T", attrs["flavor"])
		} else {
			if flavor["name"] != "m1.large" {
				t.Errorf("flavor.name = %v, expected m1.large", flavor["name"])
			}
			if flavor["vcpu"] != uint64(16) {
				t.Errorf("flavor.vcpu = %v, expected 16", flavor["vcpu"])
			}
			if flavor["ram_mib"] != uint64(4096) {
				t.Errorf("flavor.ram_mib = %v, expected 4096", flavor["ram_mib"])
			}
			if flavor["disk_gib"] != uint64(100) {
				t.Errorf("flavor.disk_gib = %v, expected 100", flavor["disk_gib"])
			}
		}

		if attrs["commitment_id"] != "commit-456" {
			t.Errorf("commitment_id = %v, expected commit-456", attrs["commitment_id"])
		}
	})

	t.Run("without commitment (PAYG)", func(t *testing.T) {
		attrs := buildVMAttributes(vm, "")

		if attrs["commitment_id"] != nil {
			t.Errorf("commitment_id = %v, expected nil", attrs["commitment_id"])
		}
	})

	t.Run("with video RAM - video_ram_mib present", func(t *testing.T) {
		vram := uint64(16)
		vmWithVRAM := vm
		vmWithVRAM.VideoRAMMiB = &vram
		attrs := buildVMAttributes(vmWithVRAM, "")

		flavor, ok := attrs["flavor"].(map[string]any)
		if !ok {
			t.Fatalf("flavor is not map[string]any: %T", attrs["flavor"])
		}
		if flavor["video_ram_mib"] != uint64(16) {
			t.Errorf("flavor.video_ram_mib = %v, expected 16", flavor["video_ram_mib"])
		}
		if _, present := flavor["hw_version"]; present {
			t.Errorf("flavor.hw_version must not appear in output")
		}
	})

	t.Run("without video RAM - video_ram_mib absent", func(t *testing.T) {
		attrs := buildVMAttributes(vm, "")

		flavor, ok := attrs["flavor"].(map[string]any)
		if !ok {
			t.Fatalf("flavor is not map[string]any: %T", attrs["flavor"])
		}
		if _, present := flavor["video_ram_mib"]; present {
			t.Errorf("flavor.video_ram_mib should be absent when VideoRAMMiB is nil, got %v", flavor["video_ram_mib"])
		}
	})
}

func TestCountCommitmentStates(t *testing.T) {
	tests := []struct {
		name     string
		input    map[string][]*CommitmentStateWithUsage
		expected int
	}{
		{
			name:     "empty map",
			input:    map[string][]*CommitmentStateWithUsage{},
			expected: 0,
		},
		{
			name: "single key with one commitment",
			input: map[string][]*CommitmentStateWithUsage{
				"az-a:hana_1": {{CommitmentState: CommitmentState{CommitmentUUID: "c1"}}},
			},
			expected: 1,
		},
		{
			name: "single key with multiple commitments",
			input: map[string][]*CommitmentStateWithUsage{
				"az-a:hana_1": {
					{CommitmentState: CommitmentState{CommitmentUUID: "c1"}},
					{CommitmentState: CommitmentState{CommitmentUUID: "c2"}},
					{CommitmentState: CommitmentState{CommitmentUUID: "c3"}},
				},
			},
			expected: 3,
		},
		{
			name: "multiple keys",
			input: map[string][]*CommitmentStateWithUsage{
				"az-a:hana_1": {
					{CommitmentState: CommitmentState{CommitmentUUID: "c1"}},
					{CommitmentState: CommitmentState{CommitmentUUID: "c2"}},
				},
				"az-b:hana_1": {
					{CommitmentState: CommitmentState{CommitmentUUID: "c3"}},
				},
				"az-a:gp_1": {
					{CommitmentState: CommitmentState{CommitmentUUID: "c4"}},
					{CommitmentState: CommitmentState{CommitmentUUID: "c5"}},
				},
			},
			expected: 5,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := countCommitmentStates(tt.input)
			if result != tt.expected {
				t.Errorf("countCommitmentStates() = %d, expected %d", result, tt.expected)
			}
		})
	}
}
func TestUsageCalculator_AssignVMsToCommitments(t *testing.T) {
	tests := []struct {
		name                string
		vms                 []VMUsageInfo
		commitments         map[string][]*CommitmentStateWithUsage
		expectedAssignments map[string]string // vmUUID -> commitmentUUID (empty = PAYG)
		expectedCount       int
	}{
		{
			name: "no VMs",
			vms:  []VMUsageInfo{},
			commitments: map[string][]*CommitmentStateWithUsage{
				"az-a:hana_1": {{CommitmentState: CommitmentState{CommitmentUUID: "c1"}, RemainingMemoryBytes: 4096 * 1024 * 1024}},
			},
			expectedAssignments: map[string]string{},
			expectedCount:       0,
		},
		{
			name: "no commitments - all PAYG",
			vms: []VMUsageInfo{
				{UUID: "vm-1", AZ: "az-a", FlavorGroup: "hana_1", MemoryMB: 1024},
				{UUID: "vm-2", AZ: "az-a", FlavorGroup: "hana_1", MemoryMB: 1024},
			},
			commitments: map[string][]*CommitmentStateWithUsage{},
			expectedAssignments: map[string]string{
				"vm-1": "",
				"vm-2": "",
			},
			expectedCount: 0,
		},
		{
			name: "VM fits in commitment",
			vms: []VMUsageInfo{
				{UUID: "vm-1", AZ: "az-a", FlavorGroup: "hana_1", MemoryMB: 1024},
			},
			commitments: map[string][]*CommitmentStateWithUsage{
				"az-a:hana_1": {{CommitmentState: CommitmentState{CommitmentUUID: "c1"}, RemainingMemoryBytes: 2 * 1024 * 1024 * 1024}},
			},
			expectedAssignments: map[string]string{
				"vm-1": "c1",
			},
			expectedCount: 1,
		},
		{
			name: "VM does not fit - goes to PAYG",
			vms: []VMUsageInfo{
				{UUID: "vm-1", AZ: "az-a", FlavorGroup: "hana_1", MemoryMB: 4096},
			},
			commitments: map[string][]*CommitmentStateWithUsage{
				"az-a:hana_1": {{CommitmentState: CommitmentState{CommitmentUUID: "c1"}, RemainingMemoryBytes: 1024 * 1024 * 1024}}, // Only 1GB capacity
			},
			expectedAssignments: map[string]string{
				"vm-1": "", // PAYG - doesn't fit
			},
			expectedCount: 0,
		},
		{
			name: "overflow to PAYG",
			vms: []VMUsageInfo{
				{UUID: "vm-1", AZ: "az-a", FlavorGroup: "hana_1", MemoryMB: 2048},
				{UUID: "vm-2", AZ: "az-a", FlavorGroup: "hana_1", MemoryMB: 2048},
				{UUID: "vm-3", AZ: "az-a", FlavorGroup: "hana_1", MemoryMB: 2048},
			},
			commitments: map[string][]*CommitmentStateWithUsage{
				"az-a:hana_1": {{CommitmentState: CommitmentState{CommitmentUUID: "c1"}, RemainingMemoryBytes: 4 * 1024 * 1024 * 1024}}, // 4GB - fits 2 VMs
			},
			expectedAssignments: map[string]string{
				"vm-1": "c1",
				"vm-2": "c1",
				"vm-3": "", // PAYG - no more capacity
			},
			expectedCount: 2,
		},
		{
			name: "different AZs - separate assignment",
			vms: []VMUsageInfo{
				{UUID: "vm-az-a", AZ: "az-a", FlavorGroup: "hana_1", MemoryMB: 1024},
				{UUID: "vm-az-b", AZ: "az-b", FlavorGroup: "hana_1", MemoryMB: 1024},
			},
			commitments: map[string][]*CommitmentStateWithUsage{
				"az-a:hana_1": {{CommitmentState: CommitmentState{CommitmentUUID: "c-az-a"}, RemainingMemoryBytes: 2 * 1024 * 1024 * 1024}},
				// No commitment in az-b
			},
			expectedAssignments: map[string]string{
				"vm-az-a": "c-az-a",
				"vm-az-b": "", // PAYG - no commitment in az-b
			},
			expectedCount: 1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assignVMsToCommitments(tt.vms, tt.commitments)

			// Derive assignment map from mutated commitment states.
			assignments := make(map[string]string)
			totalAssigned := 0
			for _, states := range tt.commitments {
				for _, state := range states {
					for _, vmUUID := range state.AssignedInstances {
						assignments[vmUUID] = state.CommitmentUUID
						totalAssigned++
					}
				}
			}

			if totalAssigned != tt.expectedCount {
				t.Errorf("assigned count = %d, expected %d", totalAssigned, tt.expectedCount)
			}

			for vmUUID, expectedCommitment := range tt.expectedAssignments {
				actual, ok := assignments[vmUUID]
				if expectedCommitment == "" {
					if ok {
						t.Errorf("VM %s should be PAYG but was assigned to %q", vmUUID, actual)
					}
				} else {
					if !ok {
						t.Errorf("VM %s not in assignments", vmUUID)
						continue
					}
					if actual != expectedCommitment {
						t.Errorf("VM %s: commitment = %q, expected %q", vmUUID, actual, expectedCommitment)
					}
				}
			}
		})
	}
}
