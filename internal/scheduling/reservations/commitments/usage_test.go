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
	"github.com/cobaltcore-dev/cortex/internal/scheduling/nova"
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
		vms           []nova.ServerDetail
		reservations  []*v1alpha1.Reservation
		allAZs        []liquid.AvailabilityZone
		expectedUsage map[string]uint64 // resourceName -> usage
	}{
		{
			name:         "empty project",
			projectID:    "project-empty",
			vms:          []nova.ServerDetail{},
			reservations: []*v1alpha1.Reservation{},
			allAZs:       []liquid.AvailabilityZone{"az-a"},
			expectedUsage: map[string]uint64{
				"hw_version_hana_1_ram": 0,
			},
		},
		{
			name:      "single VM with commitment",
			projectID: "project-A",
			vms: []nova.ServerDetail{
				{
					ID: "vm-001", Name: "vm-001", Status: "ACTIVE",
					TenantID: "project-A", AvailabilityZone: "az-a",
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
			vms: []nova.ServerDetail{
				{
					ID: "vm-002", Name: "vm-002", Status: "ACTIVE",
					TenantID: "project-B", AvailabilityZone: "az-a",
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
			novaClient := &mockUsageNovaClient{
				servers: map[string][]nova.ServerDetail{
					tt.projectID: tt.vms,
				},
			}

			// Create calculator and run
			calc := NewUsageCalculator(k8sClient, novaClient)
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
		Metadata:   map[string]string{"env": "prod"},
		Tags:       []string{"important"},
	}

	t.Run("with commitment", func(t *testing.T) {
		attrs := buildVMAttributes(vm, "commit-456")

		// Status at top level
		if attrs["status"] != "ACTIVE" {
			t.Errorf("status = %v, expected ACTIVE", attrs["status"])
		}

		// Metadata at top level
		metadata, ok := attrs["metadata"].(map[string]string)
		if !ok {
			t.Errorf("metadata is not map[string]string: %T", attrs["metadata"])
		} else if metadata["env"] != "prod" {
			t.Errorf("metadata[env] = %v, expected prod", metadata["env"])
		}

		// Tags at top level
		tags, ok := attrs["tags"].([]string)
		if !ok {
			t.Errorf("tags is not []string: %T", attrs["tags"])
		} else if len(tags) != 1 || tags[0] != "important" {
			t.Errorf("tags = %v, expected [important]", tags)
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

		// OS type (empty for now)
		if attrs["os_type"] != "" {
			t.Errorf("os_type = %v, expected empty string", attrs["os_type"])
		}
	})

	t.Run("without commitment (PAYG)", func(t *testing.T) {
		attrs := buildVMAttributes(vm, "")

		if attrs["commitment_id"] != nil {
			t.Errorf("commitment_id = %v, expected nil", attrs["commitment_id"])
		}
	})

	t.Run("with nil metadata and tags", func(t *testing.T) {
		vmEmpty := VMUsageInfo{
			UUID:       "vm-empty",
			Name:       "empty-vm",
			FlavorName: "m1.small",
			Status:     "ACTIVE",
			MemoryMB:   1024,
			VCPUs:      2,
			DiskGB:     10,
			Metadata:   nil,
			Tags:       nil,
		}
		attrs := buildVMAttributes(vmEmpty, "")

		// Should have empty map and slice, not nil (for JSON serialization)
		metadata, ok := attrs["metadata"].(map[string]string)
		if !ok || metadata == nil {
			t.Errorf("metadata should be empty map, got %T: %v", attrs["metadata"], attrs["metadata"])
		}

		tags, ok := attrs["tags"].([]string)
		if !ok || tags == nil {
			t.Errorf("tags should be empty slice, got %T: %v", attrs["tags"], attrs["tags"])
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
		vms                      []nova.ServerDetail
		reservations             []*v1alpha1.Reservation
		allAZs                   []liquid.AvailabilityZone
		expectedActiveCommitment string // non-empty if VM should be assigned to a commitment
	}{
		{
			name:      "active commitment - within time range",
			projectID: "project-A",
			vms: []nova.ServerDetail{
				{
					ID: "vm-001", Name: "vm-001", Status: "ACTIVE",
					TenantID: "project-A", AvailabilityZone: "az-a",
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
			vms: []nova.ServerDetail{
				{
					ID: "vm-001", Name: "vm-001", Status: "ACTIVE",
					TenantID: "project-A", AvailabilityZone: "az-a",
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
			vms: []nova.ServerDetail{
				{
					ID: "vm-001", Name: "vm-001", Status: "ACTIVE",
					TenantID: "project-A", AvailabilityZone: "az-a",
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
			vms: []nova.ServerDetail{
				{
					ID: "vm-001", Name: "vm-001", Status: "ACTIVE",
					TenantID: "project-A", AvailabilityZone: "az-a",
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

			novaClient := &mockUsageNovaClient{
				servers: map[string][]nova.ServerDetail{
					tt.projectID: tt.vms,
				},
			}

			calc := NewUsageCalculator(k8sClient, novaClient)
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
	name := "commitment-" + commitmentUUID + "-" + string(rune('0'+slot))

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
