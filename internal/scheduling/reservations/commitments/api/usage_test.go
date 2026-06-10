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
	"github.com/cobaltcore-dev/cortex/internal/scheduling/reservations"
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

// testUsageConfig is shared across UsageCalculator tests.
// Uses "*" catch-all so all flavor groups (hana_1, etc.) have HandlesCommitments=true for RAM.
var testUsageConfig = commitments.APIConfig{
	FlavorGroupResourceConfig: map[string]commitments.FlavorGroupResourcesConfig{
		"*": {
			RAM: commitments.RAMResourceTypeConfig{HandlesCommitments: true, HasQuota: true},
		},
	},
}

func mkVM(id, az, flavorName string, ramMiB, vcpus int64, createdAt time.Time, projectID string) reservations.VM {
	return reservations.VM{
		UUID:             id,
		Name:             id,
		Status:           "ACTIVE",
		CreatedAt:        createdAt.Format(time.RFC3339),
		AvailabilityZone: az,
		FlavorName:       flavorName,
		ProjectID:        projectID,
		Resources: map[string]resource.Quantity{
			"memory": *resource.NewQuantity(ramMiB*1024*1024, resource.BinarySI), //nolint:gosec
			"vcpus":  *resource.NewQuantity(vcpus, resource.DecimalSI),           //nolint:gosec
		},
	}
}

func TestUsageCalculator_CalculateUsage(t *testing.T) {
	log.SetLogger(zap.New(zap.WriteTo(os.Stderr), zap.UseDevMode(true)))
	ctx := context.Background()
	baseTime := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)

	m1Small := &TestFlavor{Name: "m1.small", Group: "hana_1", MemoryMB: 1024, VCPUs: 4}
	m1Large := &TestFlavor{Name: "m1.large", Group: "hana_1", MemoryMB: 4096, VCPUs: 16}

	tests := []struct {
		name          string
		projectID     string
		vms           []reservations.VM
		reservations  []*v1alpha1.Reservation
		allAZs        []liquid.AvailabilityZone
		expectedUsage map[string]uint64
	}{
		{
			name:         "empty project",
			projectID:    "project-empty",
			vms:          []reservations.VM{},
			reservations: []*v1alpha1.Reservation{},
			allAZs:       []liquid.AvailabilityZone{"az-a"},
			expectedUsage: map[string]uint64{
				"hw_version_hana_1_ram": 0,
			},
		},
		{
			name:      "single VM with commitment",
			projectID: "project-A",
			vms:       []reservations.VM{mkVM("vm-001", "az-a", "m1.large", 4096, 16, baseTime, "project-A")},
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
			name:         "VM without matching commitment - PAYG",
			projectID:    "project-B",
			vms:          []reservations.VM{mkVM("vm-002", "az-a", "m1.large", 4096, 16, baseTime, "project-B")},
			reservations: []*v1alpha1.Reservation{},
			allAZs:       []liquid.AvailabilityZone{"az-a"},
			expectedUsage: map[string]uint64{
				"hw_version_hana_1_ram": 4,
			},
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
				WithIndex(&v1alpha1.CommittedResource{}, "spec.projectID", func(obj client.Object) []string {
					cr, ok := obj.(*v1alpha1.CommittedResource)
					if !ok || cr.Spec.ProjectID == "" {
						return nil
					}
					return []string{cr.Spec.ProjectID}
				}).
				Build()

			vmSrc := &mockVMSource{vms: map[string][]reservations.VM{tt.projectID: tt.vms}}

			calc := commitments.NewUsageCalculator(k8sClient, vmSrc, testUsageConfig)
			logger := log.FromContext(ctx)
			report, err := calc.CalculateUsage(ctx, logger, tt.projectID, tt.allAZs)
			if err != nil {
				t.Fatalf("CalculateUsage failed: %v", err)
			}

			if len(report.Resources) == 0 {
				t.Error("Expected at least one resource in report")
			}

			for resourceName, expectedUsage := range tt.expectedUsage {
				res, ok := report.Resources[liquid.ResourceName(resourceName)]
				if !ok {
					t.Errorf("Resource %s not found", resourceName)
					continue
				}
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
		vms                      []reservations.VM
		reservations             []*v1alpha1.Reservation
		allAZs                   []liquid.AvailabilityZone
		expectedActiveCommitment string
	}{
		{
			name:      "active commitment - within time range",
			projectID: "project-A",
			vms:       []reservations.VM{mkVM("vm-001", "az-a", "m1.large", 4096, 16, baseTime, "project-A")},
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
			vms:       []reservations.VM{mkVM("vm-001", "az-a", "m1.large", 4096, 16, baseTime, "project-A")},
			reservations: func() []*v1alpha1.Reservation {
				past := now.Add(-2 * time.Hour)
				expired := now.Add(-1 * time.Hour)
				return []*v1alpha1.Reservation{
					makeUsageTestReservationWithTimes("commit-expired", "project-A", "hana_1", "az-a", 4*1024*1024*1024, 0, &past, &expired),
				}
			}(),
			allAZs:                   []liquid.AvailabilityZone{"az-a"},
			expectedActiveCommitment: "",
		},
		{
			name:      "future commitment - should be ignored (VM goes to PAYG)",
			projectID: "project-A",
			vms:       []reservations.VM{mkVM("vm-001", "az-a", "m1.large", 4096, 16, baseTime, "project-A")},
			reservations: func() []*v1alpha1.Reservation {
				futureStart := now.Add(1 * time.Hour)
				futureEnd := now.Add(24 * time.Hour)
				return []*v1alpha1.Reservation{
					makeUsageTestReservationWithTimes("commit-future", "project-A", "hana_1", "az-a", 4*1024*1024*1024, 0, &futureStart, &futureEnd),
				}
			}(),
			allAZs:                   []liquid.AvailabilityZone{"az-a"},
			expectedActiveCommitment: "",
		},
		{
			name:      "mixed - only active commitment is used",
			projectID: "project-A",
			vms:       []reservations.VM{mkVM("vm-001", "az-a", "m1.large", 4096, 16, baseTime, "project-A")},
			reservations: func() []*v1alpha1.Reservation {
				expiredStart := now.Add(-48 * time.Hour)
				expiredEnd := now.Add(-24 * time.Hour)
				activeStart := now.Add(-1 * time.Hour)
				activeEnd := now.Add(24 * time.Hour)
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
			expectedActiveCommitment: "commit-active",
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

			vmSrc := &mockVMSource{vms: map[string][]reservations.VM{tt.projectID: tt.vms}}

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
					Client:   k8sClient,
					Conf:     commitments.UsageReconcilerConfig{CooldownInterval: metav1.Duration{Duration: 0}},
					VMSource: vmSrc,
					Monitor:  commitments.NewUsageReconcilerMonitor(),
				}
				req := ctrl.Request{NamespacedName: types.NamespacedName{Name: cr.Name}}
				if _, err := rec.Reconcile(ctx, req); err != nil {
					t.Fatalf("usage reconciler failed for %s: %v", uuid, err)
				}
			}

			calc := commitments.NewUsageCalculator(k8sClient, vmSrc, testUsageConfig)
			logger := log.FromContext(ctx)
			report, err := calc.CalculateUsage(ctx, logger, tt.projectID, tt.allAZs)
			if err != nil {
				t.Fatalf("CalculateUsage failed: %v", err)
			}

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
					var attrMap map[string]any
					if err := json.Unmarshal(sub.Attributes, &attrMap); err != nil {
						continue
					}
					foundCommitment = attrMap["commitment_id"]
				}
			}

			if tt.expectedActiveCommitment == "" {
				if foundCommitment != nil {
					t.Errorf("Expected PAYG (nil commitment_id), got %v", foundCommitment)
				}
			} else {
				if foundCommitment != tt.expectedActiveCommitment {
					t.Errorf("Expected commitment %s, got %v", tt.expectedActiveCommitment, foundCommitment)
				}
			}
		})
	}
}

func TestUsageMultipleCalculation_FloorDivision(t *testing.T) {
	log.SetLogger(zap.New(zap.WriteTo(os.Stderr), zap.UseDevMode(true)))
	ctx := context.Background()
	baseTime := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)

	smallestFlavor := &TestFlavor{Name: "g_k_c1_m2_v2", Group: "hw_2101", MemoryMB: 2048, VCPUs: 1}
	flavor2x := &TestFlavor{Name: "g_k_c2_m4_v2", Group: "hw_2101", MemoryMB: 4096, VCPUs: 2}
	flavor8x := &TestFlavor{Name: "g_k_c4_m16_v2", Group: "hw_2101", MemoryMB: 16384, VCPUs: 4}
	flavor16x := &TestFlavor{Name: "g_k_c16_m32_v2", Group: "hw_2101", MemoryMB: 32768, VCPUs: 16}

	tests := []struct {
		name              string
		vms               []reservations.VM
		expectedRAM       uint64
		expectedCores     uint64
		expectedInstances uint64
	}{
		{
			name:              "single smallest flavor - 2 units",
			vms:               []reservations.VM{mkVM("vm-001", "az-a", "g_k_c1_m2_v2", 2048, 1, baseTime, "project-A")},
			expectedRAM:       2,
			expectedCores:     1,
			expectedInstances: 1,
		},
		{
			name:              "2x flavor - 4096/1024 = 4 GiB",
			vms:               []reservations.VM{mkVM("vm-001", "az-a", "g_k_c2_m4_v2", 4096, 2, baseTime, "project-A")},
			expectedRAM:       4,
			expectedCores:     2,
			expectedInstances: 1,
		},
		{
			name: "multiple VMs - RAM units should match cores for fixed ratio",
			vms: []reservations.VM{
				mkVM("vm-001", "az-a", "g_k_c1_m2_v2", 2048, 1, baseTime, "project-A"),
				mkVM("vm-002", "az-a", "g_k_c2_m4_v2", 4096, 2, baseTime.Add(time.Second), "project-A"),
				mkVM("vm-003", "az-a", "g_k_c4_m16_v2", 16384, 4, baseTime.Add(2*time.Second), "project-A"),
				mkVM("vm-004", "az-a", "g_k_c16_m32_v2", 32768, 16, baseTime.Add(3*time.Second), "project-A"),
			},
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

			vmSrc := &mockVMSource{vms: map[string][]reservations.VM{"project-A": tt.vms}}

			calc := commitments.NewUsageCalculator(k8sClient, vmSrc, testUsageConfig)
			logger := log.FromContext(ctx)
			report, err := calc.CalculateUsage(ctx, logger, "project-A", []liquid.AvailabilityZone{"az-a"})
			if err != nil {
				t.Fatalf("CalculateUsage failed: %v", err)
			}

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
	name := "commitment-" + commitmentUUID + "-" + string(rune('0'+slot)) //nolint:gosec

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
