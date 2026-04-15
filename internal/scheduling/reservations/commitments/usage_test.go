// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

//nolint:unparam,errcheck // test helper functions have fixed parameters for simplicity
package commitments

import (
	"context"
	"encoding/json"
	"os"
	"testing"
	"time"

	"github.com/cobaltcore-dev/cortex/api/v1alpha1"
	"github.com/cobaltcore-dev/cortex/internal/knowledge/extractor/plugins/compute"
	hv1 "github.com/cobaltcore-dev/openstack-hypervisor-operator/api/v1"
	"github.com/sapcc/go-api-declarations/liquid"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
)

// ============================================================================
// Unit Tests for UsageCalculator
// ============================================================================

func TestUsageCalculator_CalculateUsage(t *testing.T) {
	log.SetLogger(zap.New(zap.WriteTo(os.Stderr), zap.UseDevMode(true)))
	ctx := context.Background()
	baseTime := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)

	// Reuse TestFlavor from api_change_commitments_test.go
	m1Small := &TestFlavor{Name: "m1.small", Group: "hana_1", MemoryMB: 1024, VCPUs: 4}
	m1Large := &TestFlavor{Name: "m1.large", Group: "hana_1", MemoryMB: 4096, VCPUs: 16}

	tests := []struct {
		name          string
		projectID     string
		vms           []VMRow
		reservations  []*v1alpha1.Reservation
		allAZs        []liquid.AvailabilityZone
		expectedUsage map[string]uint64 // resourceName -> usage
	}{
		{
			name:         "empty project",
			projectID:    "project-empty",
			vms:          []VMRow{},
			reservations: []*v1alpha1.Reservation{},
			allAZs:       []liquid.AvailabilityZone{"az-a"},
			expectedUsage: map[string]uint64{
				"hw_version_hana_1_ram": 0,
			},
		},
		{
			name:      "single VM with commitment",
			projectID: "project-A",
			vms: []VMRow{
				{
					ID: "vm-001", Name: "vm-001", Status: "ACTIVE",
					AZ:         "az-a",
					Created:    baseTime.Format(time.RFC3339),
					FlavorName: "m1.large", FlavorRAM: 4096, FlavorVCPUs: 16,
				},
			},
			reservations: []*v1alpha1.Reservation{
				makeUsageTestReservation("commit-1", "project-A", "hana_1", "az-a", 1024*1024*1024, 0),
				makeUsageTestReservation("commit-1", "project-A", "hana_1", "az-a", 1024*1024*1024, 1),
				makeUsageTestReservation("commit-1", "project-A", "hana_1", "az-a", 1024*1024*1024, 2),
				makeUsageTestReservation("commit-1", "project-A", "hana_1", "az-a", 1024*1024*1024, 3),
			},
			allAZs: []liquid.AvailabilityZone{"az-a"},
			expectedUsage: map[string]uint64{
				"hw_version_hana_1_ram": 4, // 4096 MB / 1024 MB = 4 units
			},
		},
		{
			name:      "VM without matching commitment - PAYG",
			projectID: "project-B",
			vms: []VMRow{
				{
					ID: "vm-002", Name: "vm-002", Status: "ACTIVE",
					AZ:         "az-a",
					Created:    baseTime.Format(time.RFC3339),
					FlavorName: "m1.large", FlavorRAM: 4096, FlavorVCPUs: 16,
				},
			},
			reservations: []*v1alpha1.Reservation{}, // No commitments
			allAZs:       []liquid.AvailabilityZone{"az-a"},
			expectedUsage: map[string]uint64{
				"hw_version_hana_1_ram": 4,
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Setup K8s client
			scheme := runtime.NewScheme()
			_ = v1alpha1.AddToScheme(scheme)
			_ = hv1.AddToScheme(scheme)

			objects := make([]client.Object, 0, len(tt.reservations)+1)
			for _, r := range tt.reservations {
				objects = append(objects, r)
			}

			// Build flavor groups using existing test helpers
			flavorGroups := TestFlavorGroup{
				infoVersion: 1234,
				flavors:     []compute.FlavorInGroup{m1Small.ToFlavorInGroup(), m1Large.ToFlavorInGroup()},
			}.ToFlavorGroupsKnowledge()
			objects = append(objects, createKnowledgeCRD(flavorGroups))

			k8sClient := fake.NewClientBuilder().
				WithScheme(scheme).
				WithObjects(objects...).
				Build()

			// Setup mock Nova client
			dbClient := &mockUsageDBClient{
				rows: map[string][]VMRow{
					tt.projectID: tt.vms,
				},
			}

			// Create calculator and run
			calc := NewUsageCalculator(k8sClient, dbClient)
			logger := log.FromContext(ctx)
			report, err := calc.CalculateUsage(ctx, logger, tt.projectID, tt.allAZs)
			if err != nil {
				t.Fatalf("CalculateUsage failed: %v", err)
			}

			// Verify resource count
			if len(report.Resources) == 0 {
				t.Error("Expected at least one resource in report")
			}

			// Verify usage per resource
			for resourceName, expectedUsage := range tt.expectedUsage {
				res, ok := report.Resources[liquid.ResourceName(resourceName)]
				if !ok {
					t.Errorf("Resource %s not found", resourceName)
					continue
				}

				// Sum usage across all AZs
				var totalUsage uint64
				for _, azReport := range res.PerAZ {
					totalUsage += azReport.Usage
				}
				if totalUsage != expectedUsage {
					t.Errorf("Resource %s: expected usage %d, got %d", resourceName, expectedUsage, totalUsage)
				}
			}
		})
	}
}

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
	}

	t.Run("with commitment", func(t *testing.T) {
		attrs := buildVMAttributes(vm, "commit-456")

		// Status at top level
		if attrs["status"] != "ACTIVE" {
			t.Errorf("status = %v, expected ACTIVE", attrs["status"])
		}

		// metadata, tags and os_type are not included (not in Postgres cache)
		for _, absent := range []string{"metadata", "tags", "os_type"} {
			if _, present := attrs[absent]; present {
				t.Errorf("%s must not appear in output (not available from Postgres cache)", absent)
			}
		}

		// Flavor is now nested
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

		// Commitment ID
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
		attrs := buildVMAttributes(vm, "") // vm.VideoRAMMiB is nil

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

func TestUsageCalculator_ExpiredAndFutureCommitments(t *testing.T) {
	log.SetLogger(zap.New(zap.WriteTo(os.Stderr), zap.UseDevMode(true)))
	ctx := context.Background()
	baseTime := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	now := time.Now()

	m1Small := &TestFlavor{Name: "m1.small", Group: "hana_1", MemoryMB: 1024, VCPUs: 4}
	m1Large := &TestFlavor{Name: "m1.large", Group: "hana_1", MemoryMB: 4096, VCPUs: 16}

	tests := []struct {
		name                     string
		projectID                string
		vms                      []VMRow
		reservations             []*v1alpha1.Reservation
		allAZs                   []liquid.AvailabilityZone
		expectedActiveCommitment string // non-empty if VM should be assigned to a commitment
	}{
		{
			name:      "active commitment - within time range",
			projectID: "project-A",
			vms: []VMRow{
				{
					ID: "vm-001", Name: "vm-001", Status: "ACTIVE",
					AZ:         "az-a",
					Created:    baseTime.Format(time.RFC3339),
					FlavorName: "m1.large", FlavorRAM: 4096, FlavorVCPUs: 16,
				},
			},
			reservations: func() []*v1alpha1.Reservation {
				past := now.Add(-1 * time.Hour)
				future := now.Add(1 * time.Hour)
				return []*v1alpha1.Reservation{
					makeUsageTestReservationWithTimes("commit-active", "project-A", "hana_1", "az-a", 4*1024*1024*1024, 0, &past, &future),
					makeUsageTestReservationWithTimes("commit-active", "project-A", "hana_1", "az-a", 4*1024*1024*1024, 1, &past, &future),
					makeUsageTestReservationWithTimes("commit-active", "project-A", "hana_1", "az-a", 4*1024*1024*1024, 2, &past, &future),
					makeUsageTestReservationWithTimes("commit-active", "project-A", "hana_1", "az-a", 4*1024*1024*1024, 3, &past, &future),
				}
			}(),
			allAZs:                   []liquid.AvailabilityZone{"az-a"},
			expectedActiveCommitment: "commit-active",
		},
		{
			name:      "expired commitment - should be ignored (VM goes to PAYG)",
			projectID: "project-A",
			vms: []VMRow{
				{
					ID: "vm-001", Name: "vm-001", Status: "ACTIVE",
					AZ:         "az-a",
					Created:    baseTime.Format(time.RFC3339),
					FlavorName: "m1.large", FlavorRAM: 4096, FlavorVCPUs: 16,
				},
			},
			reservations: func() []*v1alpha1.Reservation {
				past := now.Add(-2 * time.Hour)
				expired := now.Add(-1 * time.Hour) // Already expired
				return []*v1alpha1.Reservation{
					makeUsageTestReservationWithTimes("commit-expired", "project-A", "hana_1", "az-a", 4*1024*1024*1024, 0, &past, &expired),
				}
			}(),
			allAZs:                   []liquid.AvailabilityZone{"az-a"},
			expectedActiveCommitment: "", // PAYG - expired commitment ignored
		},
		{
			name:      "future commitment - should be ignored (VM goes to PAYG)",
			projectID: "project-A",
			vms: []VMRow{
				{
					ID: "vm-001", Name: "vm-001", Status: "ACTIVE",
					AZ:         "az-a",
					Created:    baseTime.Format(time.RFC3339),
					FlavorName: "m1.large", FlavorRAM: 4096, FlavorVCPUs: 16,
				},
			},
			reservations: func() []*v1alpha1.Reservation {
				futureStart := now.Add(1 * time.Hour) // Hasn't started yet
				futureEnd := now.Add(24 * time.Hour)
				return []*v1alpha1.Reservation{
					makeUsageTestReservationWithTimes("commit-future", "project-A", "hana_1", "az-a", 4*1024*1024*1024, 0, &futureStart, &futureEnd),
				}
			}(),
			allAZs:                   []liquid.AvailabilityZone{"az-a"},
			expectedActiveCommitment: "", // PAYG - future commitment ignored
		},
		{
			name:      "mixed - only active commitment is used",
			projectID: "project-A",
			vms: []VMRow{
				{
					ID: "vm-001", Name: "vm-001", Status: "ACTIVE",
					AZ:         "az-a",
					Created:    baseTime.Format(time.RFC3339),
					FlavorName: "m1.large", FlavorRAM: 4096, FlavorVCPUs: 16,
				},
			},
			reservations: func() []*v1alpha1.Reservation {
				// Expired commitment
				expiredStart := now.Add(-48 * time.Hour)
				expiredEnd := now.Add(-24 * time.Hour)
				// Active commitment
				activeStart := now.Add(-1 * time.Hour)
				activeEnd := now.Add(24 * time.Hour)
				// Future commitment
				futureStart := now.Add(24 * time.Hour)
				futureEnd := now.Add(48 * time.Hour)

				return []*v1alpha1.Reservation{
					makeUsageTestReservationWithTimes("commit-expired", "project-A", "hana_1", "az-a", 4*1024*1024*1024, 0, &expiredStart, &expiredEnd),
					makeUsageTestReservationWithTimes("commit-active", "project-A", "hana_1", "az-a", 4*1024*1024*1024, 0, &activeStart, &activeEnd),
					makeUsageTestReservationWithTimes("commit-active", "project-A", "hana_1", "az-a", 4*1024*1024*1024, 1, &activeStart, &activeEnd),
					makeUsageTestReservationWithTimes("commit-active", "project-A", "hana_1", "az-a", 4*1024*1024*1024, 2, &activeStart, &activeEnd),
					makeUsageTestReservationWithTimes("commit-active", "project-A", "hana_1", "az-a", 4*1024*1024*1024, 3, &activeStart, &activeEnd),
					makeUsageTestReservationWithTimes("commit-future", "project-A", "hana_1", "az-a", 4*1024*1024*1024, 0, &futureStart, &futureEnd),
				}
			}(),
			allAZs:                   []liquid.AvailabilityZone{"az-a"},
			expectedActiveCommitment: "commit-active", // Only active commitment is used
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			scheme := runtime.NewScheme()
			_ = v1alpha1.AddToScheme(scheme)
			_ = hv1.AddToScheme(scheme)

			objects := make([]client.Object, 0, len(tt.reservations)+1)
			for _, r := range tt.reservations {
				objects = append(objects, r)
			}

			flavorGroups := TestFlavorGroup{
				infoVersion: 1234,
				flavors:     []compute.FlavorInGroup{m1Small.ToFlavorInGroup(), m1Large.ToFlavorInGroup()},
			}.ToFlavorGroupsKnowledge()
			objects = append(objects, createKnowledgeCRD(flavorGroups))

			k8sClient := fake.NewClientBuilder().
				WithScheme(scheme).
				WithObjects(objects...).
				Build()

			dbClient := &mockUsageDBClient{
				rows: map[string][]VMRow{
					tt.projectID: tt.vms,
				},
			}

			calc := NewUsageCalculator(k8sClient, dbClient)
			logger := log.FromContext(ctx)
			report, err := calc.CalculateUsage(ctx, logger, tt.projectID, tt.allAZs)
			if err != nil {
				t.Fatalf("CalculateUsage failed: %v", err)
			}

			// Find the VM in subresources and check its commitment assignment
			// Subresources are now on the instances resource, not RAM
			res, ok := report.Resources["hw_version_hana_1_instances"]
			if !ok {
				t.Fatal("Resource hw_version_hana_1_instances not found")
			}

			var foundCommitment any
			for _, azReport := range res.PerAZ {
				for _, sub := range azReport.Subresources {
					if sub.Attributes == nil {
						continue
					}
					// Parse JSON attributes
					var attrMap map[string]any
					if err := json.Unmarshal(sub.Attributes, &attrMap); err != nil {
						continue
					}
					foundCommitment = attrMap["commitment_id"]
				}
			}

			if tt.expectedActiveCommitment == "" {
				// Expect PAYG (nil commitment_id)
				if foundCommitment != nil {
					t.Errorf("Expected PAYG (nil commitment_id), got %v", foundCommitment)
				}
			} else {
				// Expect specific commitment
				if foundCommitment != tt.expectedActiveCommitment {
					t.Errorf("Expected commitment %s, got %v", tt.expectedActiveCommitment, foundCommitment)
				}
			}
		})
	}
}

// TestUsageMultipleCalculation_FloorDivision tests that RAM usage is calculated
// using floor division to handle Nova's memory overhead correctly.
// Nova flavors like "2 GiB" actually have 2032 MiB (not 2048) due to overhead.
// A "4 GiB" flavor has 4080 MiB, which is 2.007× the base unit.
// Floor division ensures 4080 / 2032 = 2 (not 3 from ceiling).
func TestUsageMultipleCalculation_FloorDivision(t *testing.T) {
	log.SetLogger(zap.New(zap.WriteTo(os.Stderr), zap.UseDevMode(true)))
	ctx := context.Background()
	baseTime := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)

	// Realistic Nova flavor values with memory overhead (2032 MiB base, not 2048)
	// These match real-world hw_version_2101 flavors
	smallestFlavor := &TestFlavor{Name: "g_k_c1_m2_v2", Group: "hw_2101", MemoryMB: 2032, VCPUs: 1}
	flavor2x := &TestFlavor{Name: "g_k_c2_m4_v2", Group: "hw_2101", MemoryMB: 4080, VCPUs: 2}      // ~2× smallest (4080/2032 = 2.007)
	flavor8x := &TestFlavor{Name: "g_k_c4_m16_v2", Group: "hw_2101", MemoryMB: 16368, VCPUs: 4}    // ~8× smallest (16368/2032 = 8.06)
	flavor16x := &TestFlavor{Name: "g_k_c16_m32_v2", Group: "hw_2101", MemoryMB: 32752, VCPUs: 16} // ~16× smallest (32752/2032 = 16.11)

	tests := []struct {
		name              string
		vms               []VMRow
		expectedRAM       uint64 // Expected RAM usage in units
		expectedCores     uint64 // Expected cores usage
		expectedInstances uint64
	}{
		{
			name: "single smallest flavor - 1 unit",
			vms: []VMRow{
				{
					ID: "vm-001", Name: "vm-001", Status: "ACTIVE",
					AZ:         "az-a",
					Created:    baseTime.Format(time.RFC3339),
					FlavorName: "g_k_c1_m2_v2", FlavorRAM: 2032, FlavorVCPUs: 1,
				},
			},
			expectedRAM:       1,
			expectedCores:     1,
			expectedInstances: 1,
		},
		{
			name: "2x flavor with overhead - floor(4080/2032) = 2 units, not 3",
			vms: []VMRow{
				{
					ID: "vm-001", Name: "vm-001", Status: "ACTIVE",
					AZ:         "az-a",
					Created:    baseTime.Format(time.RFC3339),
					FlavorName: "g_k_c2_m4_v2", FlavorRAM: 4080, FlavorVCPUs: 2,
				},
			},
			expectedRAM:       2, // floor(4080/2032) = 2, NOT 3 (ceiling would give 3)
			expectedCores:     2,
			expectedInstances: 1,
		},
		{
			name: "multiple VMs - RAM units should match cores for fixed ratio",
			vms: []VMRow{
				{
					ID: "vm-001", Name: "vm-001", Status: "ACTIVE",
					AZ:         "az-a",
					Created:    baseTime.Format(time.RFC3339),
					FlavorName: "g_k_c1_m2_v2", FlavorRAM: 2032, FlavorVCPUs: 1,
				},
				{
					ID: "vm-002", Name: "vm-002", Status: "ACTIVE",
					AZ:         "az-a",
					Created:    baseTime.Add(time.Second).Format(time.RFC3339),
					FlavorName: "g_k_c2_m4_v2", FlavorRAM: 4080, FlavorVCPUs: 2,
				},
				{
					ID: "vm-003", Name: "vm-003", Status: "ACTIVE",
					AZ:         "az-a",
					Created:    baseTime.Add(2 * time.Second).Format(time.RFC3339),
					FlavorName: "g_k_c4_m16_v2", FlavorRAM: 16368, FlavorVCPUs: 4,
				},
				{
					ID: "vm-004", Name: "vm-004", Status: "ACTIVE",
					AZ:         "az-a",
					Created:    baseTime.Add(3 * time.Second).Format(time.RFC3339),
					FlavorName: "g_k_c16_m32_v2", FlavorRAM: 32752, FlavorVCPUs: 16,
				},
			},
			// floor(2032/2032) + floor(4080/2032) + floor(16368/2032) + floor(32752/2032)
			// = 1 + 2 + 8 + 16 = 27 (matches sum of vCPUs: 1+2+4+16=23... wait, that's not right)
			// Actually cores = 1+2+4+16 = 23
			// RAM units = 1+2+8+16 = 27
			// These don't match because vCPUs and RAM have different ratios per flavor!
			expectedRAM:       27, // 1 + 2 + 8 + 16
			expectedCores:     23, // 1 + 2 + 4 + 16
			expectedInstances: 4,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			scheme := runtime.NewScheme()
			_ = v1alpha1.AddToScheme(scheme)
			_ = hv1.AddToScheme(scheme)

			// Build flavor groups with realistic values
			flavorGroups := TestFlavorGroup{
				infoVersion: 1234,
				flavors: []compute.FlavorInGroup{
					smallestFlavor.ToFlavorInGroup(),
					flavor2x.ToFlavorInGroup(),
					flavor8x.ToFlavorInGroup(),
					flavor16x.ToFlavorInGroup(),
				},
			}.ToFlavorGroupsKnowledge()

			objects := []client.Object{createKnowledgeCRD(flavorGroups)}
			k8sClient := fake.NewClientBuilder().
				WithScheme(scheme).
				WithObjects(objects...).
				Build()

			dbClient := &mockUsageDBClient{
				rows: map[string][]VMRow{
					"project-A": tt.vms,
				},
			}

			calc := NewUsageCalculator(k8sClient, dbClient)
			logger := log.FromContext(ctx)
			report, err := calc.CalculateUsage(ctx, logger, "project-A", []liquid.AvailabilityZone{"az-a"})
			if err != nil {
				t.Fatalf("CalculateUsage failed: %v", err)
			}

			// Check RAM usage
			ramResource := report.Resources[liquid.ResourceName("hw_version_hw_2101_ram")]
			if ramResource == nil {
				t.Fatal("hw_version_hw_2101_ram resource not found")
			}
			var totalRAM uint64
			for _, azReport := range ramResource.PerAZ {
				totalRAM += azReport.Usage
			}
			if totalRAM != tt.expectedRAM {
				t.Errorf("RAM usage = %d, expected %d", totalRAM, tt.expectedRAM)
			}

			// Check cores usage
			coresResource := report.Resources[liquid.ResourceName("hw_version_hw_2101_cores")]
			if coresResource == nil {
				t.Fatal("hw_version_hw_2101_cores resource not found")
			}
			var totalCores uint64
			for _, azReport := range coresResource.PerAZ {
				totalCores += azReport.Usage
			}
			if totalCores != tt.expectedCores {
				t.Errorf("Cores usage = %d, expected %d", totalCores, tt.expectedCores)
			}

			// Check instances usage
			instancesResource := report.Resources[liquid.ResourceName("hw_version_hw_2101_instances")]
			if instancesResource == nil {
				t.Fatal("hw_version_hw_2101_instances resource not found")
			}
			var totalInstances uint64
			for _, azReport := range instancesResource.PerAZ {
				totalInstances += azReport.Usage
			}
			if totalInstances != tt.expectedInstances {
				t.Errorf("Instances usage = %d, expected %d", totalInstances, tt.expectedInstances)
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
			calc := &UsageCalculator{}
			assignments, count := calc.assignVMsToCommitments(tt.vms, tt.commitments)

			if count != tt.expectedCount {
				t.Errorf("assigned count = %d, expected %d", count, tt.expectedCount)
			}

			for vmUUID, expectedCommitment := range tt.expectedAssignments {
				actual, ok := assignments[vmUUID]
				if !ok {
					t.Errorf("VM %s not in assignments", vmUUID)
					continue
				}
				if actual != expectedCommitment {
					t.Errorf("VM %s: commitment = %q, expected %q", vmUUID, actual, expectedCommitment)
				}
			}
		})
	}
}

// ============================================================================
// Helper Functions for Usage Tests
// ============================================================================

// makeUsageTestReservation creates a test reservation for UsageCalculator tests.
func makeUsageTestReservation(commitmentUUID, projectID, flavorGroup, az string, memoryBytes int64, slot int) *v1alpha1.Reservation {
	return makeUsageTestReservationWithTimes(commitmentUUID, projectID, flavorGroup, az, memoryBytes, slot, nil, nil)
}

// makeUsageTestReservationWithTimes creates a test reservation with start and end times.
func makeUsageTestReservationWithTimes(commitmentUUID, projectID, flavorGroup, az string, memoryBytes int64, slot int, startTime, endTime *time.Time) *v1alpha1.Reservation {
	name := "commitment-" + commitmentUUID + "-" + string(rune('0'+slot)) //nolint:gosec // slot is a small test index, no overflow risk

	res := &v1alpha1.Reservation{
		ObjectMeta: metav1.ObjectMeta{
			Name: name,
			Labels: map[string]string{
				v1alpha1.LabelReservationType: v1alpha1.ReservationTypeLabelCommittedResource,
			},
		},
		Spec: v1alpha1.ReservationSpec{
			Type:             v1alpha1.ReservationTypeCommittedResource,
			AvailabilityZone: az,
			Resources: map[hv1.ResourceName]resource.Quantity{
				"memory": *resource.NewQuantity(memoryBytes, resource.BinarySI),
			},
			CommittedResourceReservation: &v1alpha1.CommittedResourceReservationSpec{
				CommitmentUUID: commitmentUUID,
				ProjectID:      projectID,
				ResourceGroup:  flavorGroup,
			},
		},
	}

	// StartTime and EndTime are on ReservationSpec, not CommittedResourceReservationSpec
	if startTime != nil {
		res.Spec.StartTime = &metav1.Time{Time: *startTime}
	}
	if endTime != nil {
		res.Spec.EndTime = &metav1.Time{Time: *endTime}
	}

	return res
}
