// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

//nolint:unparam,errcheck // test helper functions have fixed parameters for simplicity
package api

import (
	"context"
	"encoding/json"
	"os"
	"testing"
	"time"

	"github.com/cobaltcore-dev/cortex/api/v1alpha1"
	"github.com/cobaltcore-dev/cortex/internal/knowledge/extractor/plugins/compute"
	commitments "github.com/cobaltcore-dev/cortex/internal/scheduling/reservations/commitments"
	hv1 "github.com/cobaltcore-dev/openstack-hypervisor-operator/api/v1"
	"github.com/sapcc/go-api-declarations/liquid"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
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
		vms           []commitments.VMRow
		reservations  []*v1alpha1.Reservation
		allAZs        []liquid.AvailabilityZone
		expectedUsage map[string]uint64 // resourceName -> usage
	}{
		{
			name:         "empty project",
			projectID:    "project-empty",
			vms:          []commitments.VMRow{},
			reservations: []*v1alpha1.Reservation{},
			allAZs:       []liquid.AvailabilityZone{"az-a"},
			expectedUsage: map[string]uint64{
				"hw_version_hana_1_ram": 0,
			},
		},
		{
			name:      "single VM with commitment",
			projectID: "project-A",
			vms: []commitments.VMRow{
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
			vms: []commitments.VMRow{
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
				WithIndex(&v1alpha1.CommittedResource{}, "spec.projectID", func(obj client.Object) []string {
					cr, ok := obj.(*v1alpha1.CommittedResource)
					if !ok || cr.Spec.ProjectID == "" {
						return nil
					}
					return []string{cr.Spec.ProjectID}
				}).
				Build()

			// Setup mock Nova client
			dbClient := &mockUsageDBClient{
				rows: map[string][]commitments.VMRow{
					tt.projectID: tt.vms,
				},
			}

			// Create calculator and run
			calc := commitments.NewUsageCalculator(k8sClient, dbClient)
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
		vms                      []commitments.VMRow
		reservations             []*v1alpha1.Reservation
		allAZs                   []liquid.AvailabilityZone
		expectedActiveCommitment string // non-empty if VM should be assigned to a commitment
	}{
		{
			name:      "active commitment - within time range",
			projectID: "project-A",
			vms: []commitments.VMRow{
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
			vms: []commitments.VMRow{
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
			vms: []commitments.VMRow{
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
			vms: []commitments.VMRow{
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
				WithStatusSubresource(&v1alpha1.CommittedResource{}).
				WithIndex(&v1alpha1.CommittedResource{}, "spec.commitmentUUID", func(obj client.Object) []string {
					cr, ok := obj.(*v1alpha1.CommittedResource)
					if !ok {
						return nil
					}
					return []string{cr.Spec.CommitmentUUID}
				}).
				WithIndex(&v1alpha1.CommittedResource{}, "spec.projectID", func(obj client.Object) []string {
					cr, ok := obj.(*v1alpha1.CommittedResource)
					if !ok || cr.Spec.ProjectID == "" {
						return nil
					}
					return []string{cr.Spec.ProjectID}
				}).
				Build()

			dbClient := &mockUsageDBClient{
				rows: map[string][]commitments.VMRow{
					tt.projectID: tt.vms,
				},
			}

			// Create CommittedResource CRDs and run the usage reconciler so that
			// CalculateUsage can read pre-computed assignments from CRD status.
			seen := make(map[string]bool)
			for _, r := range tt.reservations {
				if r.Spec.CommittedResourceReservation == nil {
					continue
				}
				uuid := r.Spec.CommittedResourceReservation.CommitmentUUID
				if seen[uuid] {
					continue
				}
				seen[uuid] = true
				amount := resource.MustParse("4Gi")
				spec := v1alpha1.CommittedResourceSpec{
					CommitmentUUID:   uuid,
					ProjectID:        r.Spec.CommittedResourceReservation.ProjectID,
					DomainID:         "test-domain",
					AvailabilityZone: r.Spec.AvailabilityZone,
					FlavorGroupName:  r.Spec.CommittedResourceReservation.ResourceGroup,
					ResourceType:     v1alpha1.CommittedResourceTypeMemory,
					State:            v1alpha1.CommitmentStatusConfirmed,
					Amount:           amount,
					StartTime:        r.Spec.StartTime,
					EndTime:          r.Spec.EndTime,
				}
				cr := &v1alpha1.CommittedResource{
					ObjectMeta: metav1.ObjectMeta{Name: "cr-" + uuid},
					Spec:       spec,
				}
				if err := k8sClient.Create(ctx, cr); err != nil {
					t.Fatalf("failed to create CommittedResource %s: %v", uuid, err)
				}
				cr.Status = v1alpha1.CommittedResourceStatus{
					AcceptedSpec: &spec,
					Conditions: []metav1.Condition{
						{
							Type:               v1alpha1.CommittedResourceConditionReady,
							Status:             metav1.ConditionTrue,
							Reason:             v1alpha1.CommittedResourceReasonAccepted,
							ObservedGeneration: 0,
							LastTransitionTime: metav1.Now(),
						},
					},
				}
				if err := k8sClient.Status().Update(ctx, cr); err != nil {
					t.Fatalf("failed to update CommittedResource status %s: %v", uuid, err)
				}
				rec := &commitments.UsageReconciler{
					Client:  k8sClient,
					Conf:    commitments.UsageReconcilerConfig{CooldownInterval: metav1.Duration{Duration: 0}},
					UsageDB: dbClient,
					Monitor: commitments.NewUsageReconcilerMonitor(),
				}
				req := ctrl.Request{NamespacedName: types.NamespacedName{Name: cr.Name}}
				if _, err := rec.Reconcile(ctx, req); err != nil {
					t.Fatalf("usage reconciler failed for %s: %v", uuid, err)
				}
			}

			calc := commitments.NewUsageCalculator(k8sClient, dbClient)
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
// by adding the 16 MiB video RAM reservation before dividing, matching actual flavor sizing.
// Nova flavors like "4 GiB" have 4080 MiB (4096 - 16 for hw_video:ram_max_mb=16).
// Adding 16 MiB restores the exact GiB multiple before integer division.
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
		vms               []commitments.VMRow
		expectedRAM       uint64 // Expected RAM usage in units
		expectedCores     uint64 // Expected cores usage
		expectedInstances uint64
	}{
		{
			name: "single smallest flavor - 2 units",
			vms: []commitments.VMRow{
				{
					ID: "vm-001", Name: "vm-001", Status: "ACTIVE",
					AZ:         "az-a",
					Created:    baseTime.Format(time.RFC3339),
					FlavorName: "g_k_c1_m2_v2", FlavorRAM: 2032, FlavorVCPUs: 1,
				},
			},
			expectedRAM:       2,
			expectedCores:     1,
			expectedInstances: 1,
		},
		{
			name: "2x flavor with overhead - (4080+16)/1024 = 4 GiB",
			vms: []commitments.VMRow{
				{
					ID: "vm-001", Name: "vm-001", Status: "ACTIVE",
					AZ:         "az-a",
					Created:    baseTime.Format(time.RFC3339),
					FlavorName: "g_k_c2_m4_v2", FlavorRAM: 4080, FlavorVCPUs: 2,
				},
			},
			expectedRAM:       4, // (4080+16)/1024 = 4
			expectedCores:     2,
			expectedInstances: 1,
		},
		{
			name: "multiple VMs - RAM units should match cores for fixed ratio",
			vms: []commitments.VMRow{
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
			// (2032+16)/1024 + (4080+16)/1024 + (16368+16)/1024 + (32752+16)/1024
			// = 2 + 4 + 16 + 32 = 54
			// Cores: 1 + 2 + 4 + 16 = 23
			expectedRAM:       54, // 2 + 4 + 16 + 32
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
				WithIndex(&v1alpha1.CommittedResource{}, "spec.projectID", func(obj client.Object) []string {
					cr, ok := obj.(*v1alpha1.CommittedResource)
					if !ok || cr.Spec.ProjectID == "" {
						return nil
					}
					return []string{cr.Spec.ProjectID}
				}).
				Build()

			dbClient := &mockUsageDBClient{
				rows: map[string][]commitments.VMRow{
					"project-A": tt.vms,
				},
			}

			calc := commitments.NewUsageCalculator(k8sClient, dbClient)
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

func makeUsageTestReservation(commitmentUUID, projectID, flavorGroup, az string, memoryBytes int64, slot int) *v1alpha1.Reservation {
	return makeUsageTestReservationWithTimes(commitmentUUID, projectID, flavorGroup, az, memoryBytes, slot, nil, nil)
}

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

	if startTime != nil {
		res.Spec.StartTime = &metav1.Time{Time: *startTime}
	}
	if endTime != nil {
		res.Spec.EndTime = &metav1.Time{Time: *endTime}
	}

	return res
}
