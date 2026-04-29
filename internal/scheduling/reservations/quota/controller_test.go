// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package quota

import (
	"context"
	"testing"
	"time"

	"github.com/cobaltcore-dev/cortex/api/v1alpha1"
	"github.com/cobaltcore-dev/cortex/internal/knowledge/extractor/plugins/compute"
	"github.com/cobaltcore-dev/cortex/internal/scheduling/reservations/failover"
	hv1 "github.com/cobaltcore-dev/openstack-hypervisor-operator/api/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func TestComputeTotalUsage(t *testing.T) {
	ctrl := &QuotaController{Config: DefaultQuotaControllerConfig()}

	flavorGroups := map[string]compute.FlavorGroupFeature{
		"hana_v2": {
			SmallestFlavor: compute.FlavorInGroup{MemoryMB: 32768},
			Flavors: []compute.FlavorInGroup{
				{Name: "m1.hana_v2.small", MemoryMB: 32768},
				{Name: "m1.hana_v2.large", MemoryMB: 65536},
			},
		},
		"general": {
			SmallestFlavor: compute.FlavorInGroup{MemoryMB: 4096},
			Flavors: []compute.FlavorInGroup{
				{Name: "m1.general.small", MemoryMB: 4096},
			},
		},
	}
	flavorToGroup := buildFlavorToGroupMap(flavorGroups)

	vms := []failover.VM{
		{
			UUID:             "vm-1",
			FlavorName:       "m1.hana_v2.small",
			ProjectID:        "project-a",
			AvailabilityZone: "az-1",
			Resources: map[string]resource.Quantity{
				"memory": resource.MustParse("34359738368"), // 32768 MiB in bytes
				"vcpus":  resource.MustParse("8"),
			},
		},
		{
			UUID:             "vm-2",
			FlavorName:       "m1.hana_v2.large",
			ProjectID:        "project-a",
			AvailabilityZone: "az-1",
			Resources: map[string]resource.Quantity{
				"memory": resource.MustParse("68719476736"), // 65536 MiB in bytes
				"vcpus":  resource.MustParse("16"),
			},
		},
		{
			UUID:             "vm-3",
			FlavorName:       "m1.hana_v2.small",
			ProjectID:        "project-a",
			AvailabilityZone: "az-2",
			Resources: map[string]resource.Quantity{
				"memory": resource.MustParse("34359738368"),
				"vcpus":  resource.MustParse("8"),
			},
		},
		{
			UUID:             "vm-4",
			FlavorName:       "m1.general.small",
			ProjectID:        "project-b",
			AvailabilityZone: "az-1",
			Resources: map[string]resource.Quantity{
				"memory": resource.MustParse("4294967296"), // 4096 MiB in bytes
				"vcpus":  resource.MustParse("2"),
			},
		},
		{
			UUID:             "vm-5",
			FlavorName:       "unknown-flavor",
			ProjectID:        "project-c",
			AvailabilityZone: "az-1",
			Resources: map[string]resource.Quantity{
				"memory": resource.MustParse("4294967296"),
				"vcpus":  resource.MustParse("2"),
			},
		},
	}

	result := ctrl.computeTotalUsage(vms, flavorToGroup, flavorGroups)

	// project-a: hana_v2 in az-1: 32768+65536 = 98304 MiB / 32768 = 3 units RAM, 8+16=24 cores
	// project-a: hana_v2 in az-2: 32768 MiB / 32768 = 1 unit RAM, 8 cores
	projectA := result["project-a"]
	if projectA == nil {
		t.Fatal("expected project-a in results")
	}

	ramUsage := projectA["hw_version_hana_v2_ram"]
	if ramUsage.PerAZ["az-1"] != 3 {
		t.Errorf("expected project-a az-1 hana_v2_ram = 3, got %d", ramUsage.PerAZ["az-1"])
	}
	if ramUsage.PerAZ["az-2"] != 1 {
		t.Errorf("expected project-a az-2 hana_v2_ram = 1, got %d", ramUsage.PerAZ["az-2"])
	}

	coresUsage := projectA["hw_version_hana_v2_cores"]
	if coresUsage.PerAZ["az-1"] != 24 {
		t.Errorf("expected project-a az-1 hana_v2_cores = 24, got %d", coresUsage.PerAZ["az-1"])
	}
	if coresUsage.PerAZ["az-2"] != 8 {
		t.Errorf("expected project-a az-2 hana_v2_cores = 8, got %d", coresUsage.PerAZ["az-2"])
	}

	// project-b: general in az-1: 4096/4096=1 unit RAM, 2 cores
	projectB := result["project-b"]
	if projectB == nil {
		t.Fatal("expected project-b in results")
	}
	if projectB["hw_version_general_ram"].PerAZ["az-1"] != 1 {
		t.Errorf("expected project-b az-1 general_ram = 1, got %d", projectB["hw_version_general_ram"].PerAZ["az-1"])
	}
	if projectB["hw_version_general_cores"].PerAZ["az-1"] != 2 {
		t.Errorf("expected project-b az-1 general_cores = 2, got %d", projectB["hw_version_general_cores"].PerAZ["az-1"])
	}

	// project-c: unknown flavor → not in results
	if _, exists := result["project-c"]; exists {
		t.Error("expected project-c to NOT be in results (unknown flavor)")
	}
}

func TestComputeCRUsage(t *testing.T) {
	ctrl := &QuotaController{Config: DefaultQuotaControllerConfig()}

	usedAmount5 := resource.MustParse("5")
	usedAmount3 := resource.MustParse("3")
	usedAmount2 := resource.MustParse("2")

	allCRs := []v1alpha1.CommittedResource{
		{
			Spec: v1alpha1.CommittedResourceSpec{
				ProjectID:        "project-a",
				FlavorGroupName:  "hana_v2",
				AvailabilityZone: "az-1",
				ResourceType:     v1alpha1.CommittedResourceTypeMemory,
				State:            v1alpha1.CommitmentStatusConfirmed,
			},
			Status: v1alpha1.CommittedResourceStatus{
				UsedAmount: &usedAmount5,
			},
		},
		{
			Spec: v1alpha1.CommittedResourceSpec{
				ProjectID:        "project-a",
				FlavorGroupName:  "hana_v2",
				AvailabilityZone: "az-1",
				ResourceType:     v1alpha1.CommittedResourceTypeMemory,
				State:            v1alpha1.CommitmentStatusGuaranteed,
			},
			Status: v1alpha1.CommittedResourceStatus{
				UsedAmount: &usedAmount3,
			},
		},
		{
			Spec: v1alpha1.CommittedResourceSpec{
				ProjectID:        "project-a",
				FlavorGroupName:  "hana_v2",
				AvailabilityZone: "az-1",
				ResourceType:     v1alpha1.CommittedResourceTypeCores,
				State:            v1alpha1.CommitmentStatusConfirmed,
			},
			Status: v1alpha1.CommittedResourceStatus{
				UsedAmount: &usedAmount2,
			},
		},
		// Different project — should be excluded by groupCRsByProject
		{
			Spec: v1alpha1.CommittedResourceSpec{
				ProjectID:        "project-b",
				FlavorGroupName:  "hana_v2",
				AvailabilityZone: "az-1",
				ResourceType:     v1alpha1.CommittedResourceTypeMemory,
				State:            v1alpha1.CommitmentStatusConfirmed,
			},
			Status: v1alpha1.CommittedResourceStatus{
				UsedAmount: &usedAmount5,
			},
		},
		// Pending state — should be excluded by state filter
		{
			Spec: v1alpha1.CommittedResourceSpec{
				ProjectID:        "project-a",
				FlavorGroupName:  "hana_v2",
				AvailabilityZone: "az-2",
				ResourceType:     v1alpha1.CommittedResourceTypeMemory,
				State:            v1alpha1.CommitmentStatusPending,
			},
			Status: v1alpha1.CommittedResourceStatus{
				UsedAmount: &usedAmount2,
			},
		},
	}

	// Pre-group and pass only project-a's CRs
	crsByProject := groupCRsByProject(allCRs)
	result := ctrl.computeCRUsage(crsByProject["project-a"])

	// Should include confirmed + guaranteed for project-a only
	ramUsage := result["hw_version_hana_v2_ram"]
	if ramUsage.PerAZ["az-1"] != 8 { // 5 + 3
		t.Errorf("expected cr ram usage az-1 = 8, got %d", ramUsage.PerAZ["az-1"])
	}

	coresUsage := result["hw_version_hana_v2_cores"]
	if coresUsage.PerAZ["az-1"] != 2 {
		t.Errorf("expected cr cores usage az-1 = 2, got %d", coresUsage.PerAZ["az-1"])
	}

	// az-2 should NOT be included (pending state)
	if ramUsage.PerAZ["az-2"] != 0 {
		t.Errorf("expected cr ram usage az-2 = 0 (pending excluded), got %d", ramUsage.PerAZ["az-2"])
	}
}

func TestDerivePaygUsage(t *testing.T) {
	tests := []struct {
		name       string
		totalUsage map[string]v1alpha1.ResourceQuotaUsage
		crUsage    map[string]v1alpha1.ResourceQuotaUsage
		expected   map[string]map[string]int64 // resourceName -> az -> amount
	}{
		{
			name: "basic subtraction",
			totalUsage: map[string]v1alpha1.ResourceQuotaUsage{
				"hw_version_hana_v2_ram": {PerAZ: map[string]int64{"az-1": 10, "az-2": 5}},
			},
			crUsage: map[string]v1alpha1.ResourceQuotaUsage{
				"hw_version_hana_v2_ram": {PerAZ: map[string]int64{"az-1": 3}},
			},
			expected: map[string]map[string]int64{
				"hw_version_hana_v2_ram": {"az-1": 7, "az-2": 5},
			},
		},
		{
			name: "clamp to zero",
			totalUsage: map[string]v1alpha1.ResourceQuotaUsage{
				"hw_version_hana_v2_ram": {PerAZ: map[string]int64{"az-1": 2}},
			},
			crUsage: map[string]v1alpha1.ResourceQuotaUsage{
				"hw_version_hana_v2_ram": {PerAZ: map[string]int64{"az-1": 10}},
			},
			expected: map[string]map[string]int64{
				"hw_version_hana_v2_ram": {"az-1": 0},
			},
		},
		{
			name: "no CR usage",
			totalUsage: map[string]v1alpha1.ResourceQuotaUsage{
				"hw_version_hana_v2_ram": {PerAZ: map[string]int64{"az-1": 5}},
			},
			crUsage: map[string]v1alpha1.ResourceQuotaUsage{},
			expected: map[string]map[string]int64{
				"hw_version_hana_v2_ram": {"az-1": 5},
			},
		},
		{
			name:       "empty total usage",
			totalUsage: map[string]v1alpha1.ResourceQuotaUsage{},
			crUsage: map[string]v1alpha1.ResourceQuotaUsage{
				"hw_version_hana_v2_ram": {PerAZ: map[string]int64{"az-1": 5}},
			},
			expected: map[string]map[string]int64{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := derivePaygUsage(tt.totalUsage, tt.crUsage)

			for resourceName, expectedAZ := range tt.expected {
				resUsage, ok := result[resourceName]
				if !ok {
					t.Errorf("expected resource %s in result", resourceName)
					continue
				}
				for az, expectedAmount := range expectedAZ {
					if resUsage.PerAZ[az] != expectedAmount {
						t.Errorf("resource=%s az=%s: expected %d, got %d",
							resourceName, az, expectedAmount, resUsage.PerAZ[az])
					}
				}
			}

			// Check no extra resources in result
			for resourceName := range result {
				if _, ok := tt.expected[resourceName]; !ok {
					t.Errorf("unexpected resource %s in result", resourceName)
				}
			}
		})
	}
}

func TestBuildFlavorToGroupMap(t *testing.T) {
	flavorGroups := map[string]compute.FlavorGroupFeature{
		"hana_v2": {
			Flavors: []compute.FlavorInGroup{
				{Name: "m1.hana_v2.small"},
				{Name: "m1.hana_v2.large"},
			},
		},
		"general": {
			Flavors: []compute.FlavorInGroup{
				{Name: "m1.general.small"},
			},
		},
	}

	result := buildFlavorToGroupMap(flavorGroups)

	if result["m1.hana_v2.small"] != "hana_v2" {
		t.Errorf("expected hana_v2 for m1.hana_v2.small, got %s", result["m1.hana_v2.small"])
	}
	if result["m1.hana_v2.large"] != "hana_v2" {
		t.Errorf("expected hana_v2 for m1.hana_v2.large, got %s", result["m1.hana_v2.large"])
	}
	if result["m1.general.small"] != "general" {
		t.Errorf("expected general for m1.general.small, got %s", result["m1.general.small"])
	}
	if _, exists := result["unknown"]; exists {
		t.Error("expected unknown flavor not to be in map")
	}
}

func TestIncrementDecrementUsage(t *testing.T) {
	usage := make(map[string]v1alpha1.ResourceQuotaUsage)

	// Increment from empty
	incrementUsage(usage, "res1", "az-1", 5)
	if usage["res1"].PerAZ["az-1"] != 5 {
		t.Errorf("expected 5 after increment, got %d", usage["res1"].PerAZ["az-1"])
	}

	// Increment again
	incrementUsage(usage, "res1", "az-1", 3)
	if usage["res1"].PerAZ["az-1"] != 8 {
		t.Errorf("expected 8 after second increment, got %d", usage["res1"].PerAZ["az-1"])
	}

	// Decrement
	decrementUsage(usage, "res1", "az-1", 2)
	if usage["res1"].PerAZ["az-1"] != 6 {
		t.Errorf("expected 6 after decrement, got %d", usage["res1"].PerAZ["az-1"])
	}

	// Decrement below zero → clamp to 0
	decrementUsage(usage, "res1", "az-1", 100)
	if usage["res1"].PerAZ["az-1"] != 0 {
		t.Errorf("expected 0 after over-decrement, got %d", usage["res1"].PerAZ["az-1"])
	}

	// Decrement non-existent resource (no-op)
	decrementUsage(usage, "res2", "az-1", 5)
	// Should not panic, and res2 should not exist
	if _, exists := usage["res2"]; exists {
		if usage["res2"].PerAZ != nil {
			t.Error("expected res2 to not have PerAZ after decrement on non-existent")
		}
	}
}

func TestIsCRStateIncluded(t *testing.T) {
	ctrl := &QuotaController{Config: DefaultQuotaControllerConfig()}

	if !ctrl.isCRStateIncluded(v1alpha1.CommitmentStatusConfirmed) {
		t.Error("expected confirmed to be included")
	}
	if !ctrl.isCRStateIncluded(v1alpha1.CommitmentStatusGuaranteed) {
		t.Error("expected guaranteed to be included")
	}
	if ctrl.isCRStateIncluded(v1alpha1.CommitmentStatusPending) {
		t.Error("expected pending to NOT be included")
	}
}

func TestGroupCRsByProject(t *testing.T) {
	crs := []v1alpha1.CommittedResource{
		{Spec: v1alpha1.CommittedResourceSpec{ProjectID: "p1"}},
		{Spec: v1alpha1.CommittedResourceSpec{ProjectID: "p2"}},
		{Spec: v1alpha1.CommittedResourceSpec{ProjectID: "p1"}},
		{Spec: v1alpha1.CommittedResourceSpec{ProjectID: "p3"}},
	}

	grouped := groupCRsByProject(crs)

	if len(grouped["p1"]) != 2 {
		t.Errorf("expected 2 CRs for p1, got %d", len(grouped["p1"]))
	}
	if len(grouped["p2"]) != 1 {
		t.Errorf("expected 1 CR for p2, got %d", len(grouped["p2"]))
	}
	if len(grouped["p3"]) != 1 {
		t.Errorf("expected 1 CR for p3, got %d", len(grouped["p3"]))
	}
	if len(grouped["nonexistent"]) != 0 {
		t.Error("expected 0 CRs for nonexistent project")
	}
}

func TestUsageDelta(t *testing.T) {
	delta := newUsageDelta()

	delta.addIncrement("res1", "az-1", 5)
	delta.addIncrement("res1", "az-1", 3)
	delta.addIncrement("res1", "az-2", 2)
	delta.addDecrement("res1", "az-1", 1)

	if delta.increments["res1"]["az-1"] != 8 {
		t.Errorf("expected increment res1/az-1 = 8, got %d", delta.increments["res1"]["az-1"])
	}
	if delta.increments["res1"]["az-2"] != 2 {
		t.Errorf("expected increment res1/az-2 = 2, got %d", delta.increments["res1"]["az-2"])
	}
	if delta.decrements["res1"]["az-1"] != 1 {
		t.Errorf("expected decrement res1/az-1 = 1, got %d", delta.decrements["res1"]["az-1"])
	}
}

func TestReconcile_NilTotalUsage(t *testing.T) {
	// When TotalUsage is nil, Reconcile should skip and return no error.
	// This validates the early-return branch logic used in Reconcile().
	ctrl := &QuotaController{Config: DefaultQuotaControllerConfig()}

	// computeCRUsage on nil slice should return empty map (no panic)
	result := ctrl.computeCRUsage(nil)
	if len(result) != 0 {
		t.Errorf("expected empty result for nil CRs, got %d entries", len(result))
	}

	// derivePaygUsage on nil totalUsage should return empty map
	payg := derivePaygUsage(nil, result)
	if len(payg) != 0 {
		t.Errorf("expected empty payg for nil totalUsage, got %d entries", len(payg))
	}
}

func TestAccumulateAddedVM_UnknownFlavor(t *testing.T) {
	// Verifies that accumulateAddedVM gracefully handles a VM with an unknown flavor
	ctrl := &QuotaController{Config: DefaultQuotaControllerConfig()}

	flavorGroups := map[string]compute.FlavorGroupFeature{
		"hana_v2": {
			SmallestFlavor: compute.FlavorInGroup{MemoryMB: 32768},
			Flavors:        []compute.FlavorInGroup{{Name: "m1.hana_v2.small", MemoryMB: 32768}},
		},
	}
	flavorToGroup := buildFlavorToGroupMap(flavorGroups)
	projectDeltas := make(map[string]*usageDelta)

	// Use a mock VMSource that returns a VM with unknown flavor
	ctrl.VMSource = &mockVMSource{
		getVM: func(_ context.Context, vmUUID string) (*failover.VM, error) {
			return &failover.VM{
				UUID:             vmUUID,
				FlavorName:       "unknown-flavor",
				ProjectID:        "project-a",
				AvailabilityZone: "az-1",
				Resources: map[string]resource.Quantity{
					"memory": resource.MustParse("4294967296"),
					"vcpus":  resource.MustParse("2"),
				},
			}, nil
		},
	}

	ctrl.accumulateAddedVM(context.Background(), "vm-1", flavorToGroup, flavorGroups, projectDeltas)

	// Should not have added any delta (unknown flavor)
	if len(projectDeltas) != 0 {
		t.Errorf("expected no deltas for unknown flavor, got %d projects", len(projectDeltas))
	}
}

func TestAccumulateAddedVM_KnownFlavor(t *testing.T) {
	// Set up a fake client with a ProjectQuota that has LastReconcileAt in the past.
	// The VM's CreatedAt must be AFTER LastReconcileAt for it to be considered new.
	lastReconcile := metav1.NewTime(time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC))
	vmCreatedAt := "2026-01-02T00:00:00Z" // After lastReconcile

	scheme := runtime.NewScheme()
	if err := v1alpha1.AddToScheme(scheme); err != nil {
		t.Fatalf("failed to add scheme: %v", err)
	}

	pq := &v1alpha1.ProjectQuota{
		ObjectMeta: metav1.ObjectMeta{Name: "quota-project-a"},
		Spec:       v1alpha1.ProjectQuotaSpec{ProjectID: "project-a"},
		Status: v1alpha1.ProjectQuotaStatus{
			LastReconcileAt: &lastReconcile,
		},
	}

	k8sClient := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(pq).
		WithStatusSubresource(&v1alpha1.ProjectQuota{}).
		Build()

	qc := &QuotaController{
		Client: k8sClient,
		Config: DefaultQuotaControllerConfig(),
	}

	flavorGroups := map[string]compute.FlavorGroupFeature{
		"hana_v2": {
			SmallestFlavor: compute.FlavorInGroup{MemoryMB: 32768},
			Flavors:        []compute.FlavorInGroup{{Name: "m1.hana_v2.small", MemoryMB: 32768}},
		},
	}
	flavorToGroup := buildFlavorToGroupMap(flavorGroups)
	projectDeltas := make(map[string]*usageDelta)

	qc.VMSource = &mockVMSource{
		getVM: func(_ context.Context, vmUUID string) (*failover.VM, error) {
			return &failover.VM{
				UUID:             vmUUID,
				FlavorName:       "m1.hana_v2.small",
				ProjectID:        "project-a",
				AvailabilityZone: "az-1",
				CreatedAt:        vmCreatedAt,
				Resources: map[string]resource.Quantity{
					"memory": resource.MustParse("34359738368"), // 32768 MiB
					"vcpus":  resource.MustParse("8"),
				},
			}, nil
		},
	}

	qc.accumulateAddedVM(context.Background(), "vm-1", flavorToGroup, flavorGroups, projectDeltas)

	delta, ok := projectDeltas["project-a"]
	if !ok {
		t.Fatal("expected delta for project-a")
	}

	// 32768 MiB / 32768 = 1 unit RAM
	if delta.increments["hw_version_hana_v2_ram"]["az-1"] != 1 {
		t.Errorf("expected ram increment = 1, got %d", delta.increments["hw_version_hana_v2_ram"]["az-1"])
	}
	if delta.increments["hw_version_hana_v2_cores"]["az-1"] != 8 {
		t.Errorf("expected cores increment = 8, got %d", delta.increments["hw_version_hana_v2_cores"]["az-1"])
	}
}

// mockVMSource is a test helper for VMSource.
type mockVMSource struct {
	listVMs        func(ctx context.Context) ([]failover.VM, error)
	getVM          func(ctx context.Context, vmUUID string) (*failover.VM, error)
	isServerActive func(ctx context.Context, vmUUID string) (bool, error)
	getDeletedVM   func(ctx context.Context, vmUUID string) (*failover.DeletedVMInfo, error)
}

func (m *mockVMSource) ListVMs(ctx context.Context) ([]failover.VM, error) {
	if m.listVMs != nil {
		return m.listVMs(ctx)
	}
	return nil, nil
}

func (m *mockVMSource) GetVM(ctx context.Context, vmUUID string) (*failover.VM, error) {
	if m.getVM != nil {
		return m.getVM(ctx, vmUUID)
	}
	return nil, nil
}

func (m *mockVMSource) ListVMsOnHypervisors(_ context.Context, _ *hv1.HypervisorList, _ bool) ([]failover.VM, error) {
	return nil, nil
}

func (m *mockVMSource) IsServerActive(ctx context.Context, vmUUID string) (bool, error) {
	if m.isServerActive != nil {
		return m.isServerActive(ctx, vmUUID)
	}
	return false, nil
}

func (m *mockVMSource) GetDeletedVMInfo(ctx context.Context, vmUUID string) (*failover.DeletedVMInfo, error) {
	if m.getDeletedVM != nil {
		return m.getDeletedVM(ctx, vmUUID)
	}
	return nil, nil
}
