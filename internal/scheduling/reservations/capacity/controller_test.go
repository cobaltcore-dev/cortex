// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package capacity

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"regexp"
	"sort"
	"testing"
	"time"

	hv1 "github.com/cobaltcore-dev/openstack-hypervisor-operator/api/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	schedulerapi "github.com/cobaltcore-dev/cortex/api/external/nova"
	"github.com/cobaltcore-dev/cortex/api/v1alpha1"
	"github.com/cobaltcore-dev/cortex/internal/knowledge/extractor/plugins/compute"
	"github.com/cobaltcore-dev/cortex/internal/scheduling/reservations"
)

// newTestScheme returns a runtime.Scheme with all required types registered.
func newTestScheme(t *testing.T) *runtime.Scheme {
	t.Helper()
	s := runtime.NewScheme()
	if err := v1alpha1.AddToScheme(s); err != nil {
		t.Fatalf("failed to add v1alpha1 scheme: %v", err)
	}
	if err := hv1.AddToScheme(s); err != nil {
		t.Fatalf("failed to add hypervisor scheme: %v", err)
	}
	return s
}

// newFlavorGroupKnowledge creates a ready Knowledge CRD with a single flavor group.
func newFlavorGroupKnowledge(t *testing.T, groupName string, smallestMemoryMB uint64) *v1alpha1.Knowledge {
	t.Helper()
	smallestFlavor := compute.FlavorInGroup{
		Name:       groupName + "-small",
		MemoryMB:   smallestMemoryMB,
		VCPUs:      2,
		ExtraSpecs: map[string]string{"hw:cpu_policy": "dedicated"},
	}
	features := []compute.FlavorGroupFeature{
		{
			Name:           groupName,
			SmallestFlavor: smallestFlavor,
			Flavors:        []compute.FlavorInGroup{smallestFlavor},
		},
	}
	raw, err := v1alpha1.BoxFeatureList(features)
	if err != nil {
		t.Fatalf("failed to box features: %v", err)
	}
	return &v1alpha1.Knowledge{
		ObjectMeta: metav1.ObjectMeta{Name: "flavor-groups"},
		Spec: v1alpha1.KnowledgeSpec{
			SchedulingDomain: v1alpha1.SchedulingDomainNova,
			Extractor:        v1alpha1.KnowledgeExtractorSpec{Name: "flavor_groups"},
		},
		Status: v1alpha1.KnowledgeStatus{
			Raw: raw,
			Conditions: []metav1.Condition{
				{
					Type:   v1alpha1.KnowledgeConditionReady,
					Status: metav1.ConditionTrue,
					Reason: "ExtractorSucceeded",
				},
			},
		},
	}
}

// newHypervisor creates a Hypervisor CRD with a topology AZ label, memory and CPU effective capacity.
func newHypervisor(name, az string, memoryBytes int64, instanceIDs ...string) *hv1.Hypervisor {
	hv := &hv1.Hypervisor{
		ObjectMeta: metav1.ObjectMeta{
			Name:   name,
			Labels: map[string]string{"topology.kubernetes.io/zone": az},
		},
	}
	if memoryBytes > 0 {
		memQty := resource.NewQuantity(memoryBytes, resource.BinarySI)
		cpuQty := resource.NewQuantity(128, resource.DecimalSI) // generous CPU so memory is the binding constraint
		hv.Status.EffectiveCapacity = map[hv1.ResourceName]resource.Quantity{
			hv1.ResourceMemory: *memQty,
			hv1.ResourceCPU:    *cpuQty,
		}
	}
	for _, id := range instanceIDs {
		hv.Status.Instances = append(hv.Status.Instances, hv1.Instance{ID: id})
	}
	return hv
}

// newMockSchedulerServer creates an httptest server that always returns the given host list.
func newMockSchedulerServer(t *testing.T, hosts []string) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		resp := schedulerapi.ExternalSchedulerResponse{Hosts: hosts}
		if err := json.NewEncoder(w).Encode(resp); err != nil {
			t.Errorf("mock scheduler: failed to encode response: %v", err)
		}
	}))
}

// newController is a test helper that creates a Reconciler with a nil VMSource.
func newController(t *testing.T, c client.Client, cfg Config) *Reconciler {
	t.Helper()
	return NewController(c, cfg, nil)
}

// --- unit tests for pure helper functions ---

var (
	dnsLabelRE   = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,61}[a-z0-9]$`)
	hashSuffixRE = regexp.MustCompile(`^[0-9a-f]{6}$`)
)

func TestCrdNameFor(t *testing.T) {
	tests := []struct {
		group, az  string
		wantPrefix string
	}{
		{"hana-v2", "qa-de-1a", "hana-v2-qa-de-1a-"},
		{"My_Group", "eu.west.1", "my-group-eu-west-1-"},
		{"G", "AZ_1", "g-az-1-"},
	}
	for _, tt := range tests {
		got := crdNameFor(tt.group, tt.az)
		// Must be a valid DNS label (lowercase, hyphens, ≤63 chars).
		if len(got) > 63 {
			t.Errorf("crdNameFor(%q, %q) = %q (len=%d > 63)", tt.group, tt.az, got, len(got))
		}
		if !dnsLabelRE.MatchString(got) {
			t.Errorf("crdNameFor(%q, %q) = %q is not a valid DNS label", tt.group, tt.az, got)
		}
		// Must start with the expected sanitised prefix followed by a 6-hex-char hash suffix.
		if len(got) < len(tt.wantPrefix)+6 || got[:len(tt.wantPrefix)] != tt.wantPrefix {
			t.Errorf("crdNameFor(%q, %q) = %q, want prefix %q + 6 hex chars", tt.group, tt.az, got, tt.wantPrefix)
		}
		hashPart := got[len(tt.wantPrefix):]
		if !hashSuffixRE.MatchString(hashPart) {
			t.Errorf("crdNameFor(%q, %q) hash suffix %q is not 6 hex chars", tt.group, tt.az, hashPart)
		}
	}

	// Inputs that differ only by "." vs "-" must produce different CRD names.
	dotName := crdNameFor("hana.v2", "qa-de-1a")
	dashName := crdNameFor("hana-v2", "qa-de-1a")
	if dotName == dashName {
		t.Errorf("crdNameFor collision: hana.v2 and hana-v2 both produced %q", dotName)
	}
}

func TestAvailabilityZones(t *testing.T) {
	hvs := []hv1.Hypervisor{
		*newHypervisor("h1", "az-a", 0),
		*newHypervisor("h2", "az-b", 0),
		*newHypervisor("h3", "az-a", 0),             // duplicate
		{ObjectMeta: metav1.ObjectMeta{Name: "h4"}}, // no label
	}
	got := availabilityZones(hvs)
	want := []string{"az-a", "az-b"}
	if len(got) != len(want) {
		t.Fatalf("availabilityZones() = %v, want %v", got, want)
	}
	sort.Strings(got)
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("availabilityZones()[%d] = %q, want %q", i, got[i], want[i])
		}
	}
}

func TestCountInstancesInAZ(t *testing.T) {
	// countInstancesInAZ was removed since TotalInstances is no longer stored in the CRD.
	// The AZ instance count is now derived from RunningInstances per flavor group.
	// This test is replaced by the reconcileAZ integration tests which verify RunningInstances.
	t.Skip("countInstancesInAZ removed — see TestReconcileAZ_CreatesCRD")
}

// --- integration-style tests for reconcileAZ ---

func TestReconcileAZ_CreatesCRD(t *testing.T) {
	const (
		groupName = "hana-v2"
		az        = "qa-de-1a"
		memMB     = 4096 // 4 GiB
		memBytes  = int64(memMB) * 1024 * 1024
	)

	scheme := newTestScheme(t)
	hv := newHypervisor("host-1", az, memBytes, "vm1")
	knowledge := newFlavorGroupKnowledge(t, groupName, memMB)

	fakeClient := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(knowledge, hv).
		WithStatusSubresource(&v1alpha1.FlavorGroupCapacity{}, &v1alpha1.Knowledge{}).
		Build()

	// Both probes return host-1 → total capacity = floor(4GiB/4GiB) = 1, placeable = 1.
	schedulerServer := newMockSchedulerServer(t, []string{"host-1"})
	defer schedulerServer.Close()

	ctrl := newController(t, fakeClient, Config{
		SchedulerURL:      schedulerServer.URL,
		TotalPipeline:     "kvm-report-capacity",
		PlaceablePipeline: "kvm-general-purpose",
	})

	smallFlavor := compute.FlavorInGroup{Name: groupName + "-small", MemoryMB: memMB, VCPUs: 2}
	groupData := compute.FlavorGroupFeature{
		SmallestFlavor: smallFlavor,
		Flavors:        []compute.FlavorInGroup{smallFlavor},
	}
	hvByName := map[string]hv1.Hypervisor{"host-1": *hv}

	if err := ctrl.reconcileAZ(context.Background(), az,
		map[string]compute.FlavorGroupFeature{groupName: groupData},
		hvByName, map[string]int64{}, map[vmUsageKey]vmUsage{}); err != nil {
		t.Fatalf("reconcileAZ failed: %v", err)
	}

	var crd v1alpha1.FlavorGroupCapacity
	if err := fakeClient.Get(context.Background(), types.NamespacedName{Name: crdNameFor(groupName, az)}, &crd); err != nil {
		t.Fatalf("failed to get CRD: %v", err)
	}
	if len(crd.Status.Flavors) != 1 {
		t.Fatalf("len(Status.Flavors) = %d, want 1", len(crd.Status.Flavors))
	}
	f := crd.Status.Flavors[0]
	if f.FlavorName != groupName+"-small" {
		t.Errorf("FlavorName = %q, want %q", f.FlavorName, groupName+"-small")
	}
	if f.TotalCapacityVMSlots != 1 {
		t.Errorf("TotalCapacityVMSlots = %d, want 1", f.TotalCapacityVMSlots)
	}
	if f.TotalCapacityHosts != 1 {
		t.Errorf("TotalCapacityHosts = %d, want 1", f.TotalCapacityHosts)
	}
	if f.PlaceableVMs != 1 {
		t.Errorf("PlaceableVMs = %d, want 1", f.PlaceableVMs)
	}
	if f.PlaceableHosts != 1 {
		t.Errorf("PlaceableHosts = %d, want 1", f.PlaceableHosts)
	}
	// Round-robin assigns the 1 available slot → ExclusivelyFreeCapacity[memory] = 1 flavor slot worth.
	excl := crd.Status.ExclusivelyFreeCapacity[string(v1alpha1.CommittedResourceTypeMemory)]
	if excl.IsZero() {
		t.Errorf("ExclusivelyFreeCapacity[memory] is zero, want non-zero (1 slot assigned)")
	}
	// TotalInstances removed; per-group running VMs sourced from VMSource (nil in this test → 0).
	if crd.Status.RunningInstances != 0 {
		t.Errorf("RunningInstances = %d, want 0 (no VMSource configured)", crd.Status.RunningInstances)
	}
}

func TestReconcileAZ_SkipsCRDWriteOnSchedulerError(t *testing.T) {
	const (
		groupName = "hana-v2"
		az        = "qa-de-1a"
		memMB     = 2048
	)

	scheme := newTestScheme(t)
	knowledge := newFlavorGroupKnowledge(t, groupName, memMB)

	fakeClient := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(knowledge).
		WithStatusSubresource(&v1alpha1.FlavorGroupCapacity{}, &v1alpha1.Knowledge{}).
		Build()

	// Scheduler returns 500 to simulate error.
	failServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer failServer.Close()

	ctrl := newController(t, fakeClient, Config{
		SchedulerURL:      failServer.URL,
		TotalPipeline:     "kvm-report-capacity",
		PlaceablePipeline: "kvm-general-purpose",
	})

	smallFlavor := compute.FlavorInGroup{Name: groupName + "-small", MemoryMB: memMB, VCPUs: 2}
	groupData := compute.FlavorGroupFeature{
		SmallestFlavor: smallFlavor,
		Flavors:        []compute.FlavorInGroup{smallFlavor},
	}

	if err := ctrl.reconcileAZ(context.Background(), az,
		map[string]compute.FlavorGroupFeature{groupName: groupData},
		map[string]hv1.Hypervisor{}, map[string]int64{}, map[vmUsageKey]vmUsage{}); err != nil {
		t.Fatalf("reconcileAZ failed: %v", err)
	}

	// Stale probes → CRD must NOT be written; last good state is preserved.
	var list v1alpha1.FlavorGroupCapacityList
	if err := fakeClient.List(context.Background(), &list); err != nil {
		t.Fatalf("failed to list CRDs: %v", err)
	}
	if len(list.Items) != 0 {
		t.Errorf("expected 0 CRDs (stale cycle skips write), got %d", len(list.Items))
	}
}

func TestReconcileAZ_IdempotentUpdate(t *testing.T) {
	const (
		groupName = "hana-v2"
		az        = "qa-de-1a"
		memMB     = 2048
		memBytes  = int64(memMB) * 1024 * 1024
	)

	scheme := newTestScheme(t)
	hv := newHypervisor("host-1", az, memBytes)
	knowledge := newFlavorGroupKnowledge(t, groupName, memMB)
	crdName := crdNameFor(groupName, az)

	// Pre-create the CRD to test the update path (not create path).
	existing := &v1alpha1.FlavorGroupCapacity{
		ObjectMeta: metav1.ObjectMeta{Name: crdName},
		Spec: v1alpha1.FlavorGroupCapacitySpec{
			FlavorGroup:      groupName,
			AvailabilityZone: az,
		},
	}

	fakeClient := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(knowledge, hv, existing).
		WithStatusSubresource(&v1alpha1.FlavorGroupCapacity{}, &v1alpha1.Knowledge{}).
		Build()

	schedulerServer := newMockSchedulerServer(t, []string{"host-1"})
	defer schedulerServer.Close()

	ctrl := newController(t, fakeClient, Config{
		SchedulerURL:      schedulerServer.URL,
		TotalPipeline:     "kvm-report-capacity",
		PlaceablePipeline: "kvm-general-purpose",
	})

	smallFlavor := compute.FlavorInGroup{Name: groupName + "-small", MemoryMB: memMB, VCPUs: 2}
	groupData := compute.FlavorGroupFeature{
		SmallestFlavor: smallFlavor,
		Flavors:        []compute.FlavorInGroup{smallFlavor},
	}
	hvByName := map[string]hv1.Hypervisor{"host-1": *hv}
	groups := map[string]compute.FlavorGroupFeature{groupName: groupData}

	// First call
	if err := ctrl.reconcileAZ(context.Background(), az, groups, hvByName, map[string]int64{}, map[vmUsageKey]vmUsage{}); err != nil {
		t.Fatalf("first reconcileAZ failed: %v", err)
	}
	// Second call — should not error on the already-existing CRD.
	if err := ctrl.reconcileAZ(context.Background(), az, groups, hvByName, map[string]int64{}, map[vmUsageKey]vmUsage{}); err != nil {
		t.Fatalf("second reconcileAZ failed: %v", err)
	}

	var crd v1alpha1.FlavorGroupCapacity
	if err := fakeClient.Get(context.Background(), types.NamespacedName{Name: crdName}, &crd); err != nil {
		t.Fatalf("failed to get CRD: %v", err)
	}
	if len(crd.Status.Flavors) != 1 {
		t.Fatalf("len(Status.Flavors) = %d, want 1", len(crd.Status.Flavors))
	}
	if crd.Status.Flavors[0].TotalCapacityVMSlots != 1 {
		t.Errorf("TotalCapacityVMSlots = %d, want 1", crd.Status.Flavors[0].TotalCapacityVMSlots)
	}
}

func TestReconcileAll_SkipsGroupsWithNoAZs(t *testing.T) {
	scheme := newTestScheme(t)
	knowledge := newFlavorGroupKnowledge(t, "hana-v2", 2048)

	// No hypervisors → no AZs → reconcileAll returns without error.
	fakeClient := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(knowledge).
		WithStatusSubresource(&v1alpha1.FlavorGroupCapacity{}, &v1alpha1.Knowledge{}).
		Build()

	ctrl := newController(t, fakeClient, Config{
		SchedulerURL:      "http://localhost:9999", // unreachable; not called
		TotalPipeline:     "kvm-report-capacity",
		PlaceablePipeline: "kvm-general-purpose",
	})

	if err := ctrl.reconcileAll(context.Background()); err != nil {
		t.Errorf("reconcileAll with no hypervisors returned error: %v", err)
	}

	var list v1alpha1.FlavorGroupCapacityList
	if err := fakeClient.List(context.Background(), &list); err != nil {
		t.Fatalf("failed to list CRDs: %v", err)
	}
	if len(list.Items) != 0 {
		t.Errorf("expected 0 CRDs, got %d", len(list.Items))
	}
}

func TestProbeScheduler_CapacityCalculation(t *testing.T) {
	const memMB = 4096
	const memBytes = int64(memMB) * 1024 * 1024

	scheme := newTestScheme(t)
	hv1Obj := newHypervisor("host-1", "az-a", memBytes)
	hv2Obj := newHypervisor("host-2", "az-a", memBytes*2) // 2x memory

	fakeClient := fake.NewClientBuilder().WithScheme(scheme).Build()

	// Scheduler returns both hosts.
	srv := newMockSchedulerServer(t, []string{"host-1", "host-2"})
	defer srv.Close()

	c := newController(t, fakeClient, Config{SchedulerURL: srv.URL})
	hvByName := map[string]hv1.Hypervisor{
		"host-1": *hv1Obj,
		"host-2": *hv2Obj,
	}
	flavor := compute.FlavorInGroup{Name: "test-flavor", MemoryMB: memMB}

	capacity, hosts, candidates, err := c.probeScheduler(context.Background(), flavor, "az-a", "test-pipeline", hvByName, true, nil)
	if err != nil {
		t.Fatalf("probeScheduler failed: %v", err)
	}
	if hosts != 2 {
		t.Errorf("hosts = %d, want 2", hosts)
	}
	// host-1 = 1 slot (4GiB/4GiB), host-2 = 2 slots (8GiB/4GiB).
	if capacity != 3 {
		t.Errorf("capacity = %d, want 3", capacity)
	}
	// Both hosts should appear in the candidate list.
	candidateSet := make(map[string]struct{}, len(candidates))
	for _, h := range candidates {
		candidateSet[h] = struct{}{}
	}
	if _, ok := candidateSet["host-1"]; !ok {
		t.Errorf("host-1 missing from candidates %v", candidates)
	}
	if _, ok := candidateSet["host-2"]; !ok {
		t.Errorf("host-2 missing from candidates %v", candidates)
	}
}

// TestProbeScheduler_SubtractsAllocationsWhenNotIgnored verifies that placeable-probe slot
// counting uses remaining capacity (effectiveCapacity − allocation) while the total-probe uses
// raw capacity.
func TestProbeScheduler_SubtractsAllocationsWhenNotIgnored(t *testing.T) {
	const memMB = 4096
	const memBytes = int64(memMB) * 1024 * 1024

	scheme := newTestScheme(t)

	// Host has 2-slot capacity (2 × flavor), with 1 slot already used by a running VM.
	hv := newHypervisor("host-1", "az-a", memBytes*2)
	hv.Status.Allocation = map[hv1.ResourceName]resource.Quantity{
		hv1.ResourceMemory: *resource.NewQuantity(memBytes, resource.BinarySI),
	}

	fakeClient := fake.NewClientBuilder().WithScheme(scheme).Build()
	srv := newMockSchedulerServer(t, []string{"host-1"})
	defer srv.Close()

	c := newController(t, fakeClient, Config{SchedulerURL: srv.URL})
	hvByName := map[string]hv1.Hypervisor{"host-1": *hv}
	flavor := compute.FlavorInGroup{Name: "test-flavor", MemoryMB: memMB}

	// Total probe (ignoreAllocations=true): raw capacity → 2 slots.
	totalCap, _, _, err := c.probeScheduler(context.Background(), flavor, "az-a", "total-pipeline", hvByName, true, nil)
	if err != nil {
		t.Fatalf("probeScheduler (total) failed: %v", err)
	}
	if totalCap != 2 {
		t.Errorf("total capacity = %d, want 2 (raw slots)", totalCap)
	}

	// Placeable probe (ignoreAllocations=false): capacity − allocation → 1 slot.
	placeableCap, _, _, err := c.probeScheduler(context.Background(), flavor, "az-a", "placeable-pipeline", hvByName, false, nil)
	if err != nil {
		t.Fatalf("probeScheduler (placeable) failed: %v", err)
	}
	if placeableCap != 1 {
		t.Errorf("placeable capacity = %d, want 1 (remaining slot after running VM)", placeableCap)
	}
}

func TestReconcileAll_MultipleGroupsAndAZs(t *testing.T) {
	scheme := newTestScheme(t)

	const memMB = 2048
	const memBytes = int64(memMB) * 1024 * 1024

	// Two AZs, two hypervisors.
	hv1Obj := newHypervisor("h1", "az-a", memBytes)
	hv2Obj := newHypervisor("h2", "az-b", memBytes)
	knowledge := newFlavorGroupKnowledge(t, "2152", memMB)

	fakeClient := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(knowledge, hv1Obj, hv2Obj).
		WithStatusSubresource(&v1alpha1.FlavorGroupCapacity{}, &v1alpha1.Knowledge{}).
		Build()

	srv := newMockSchedulerServer(t, []string{})
	defer srv.Close()

	c := newController(t, fakeClient, Config{
		SchedulerURL:      srv.URL,
		TotalPipeline:     "kvm-report-capacity",
		PlaceablePipeline: "kvm-general-purpose",
	})

	if err := c.reconcileAll(context.Background()); err != nil {
		t.Fatalf("reconcileAll failed: %v", err)
	}

	// Expect one CRD per AZ for the single group.
	var list v1alpha1.FlavorGroupCapacityList
	if err := fakeClient.List(context.Background(), &list); err != nil {
		t.Fatalf("failed to list CRDs: %v", err)
	}
	if len(list.Items) != 2 {
		names := make([]string, len(list.Items))
		for i, item := range list.Items {
			names[i] = item.Name
		}
		t.Errorf("expected 2 CRDs (one per AZ), got %d: %v", len(list.Items), names)
	}
}

func TestReconcileAll_FlavorGroupsKnowledgeNotReady(t *testing.T) {
	scheme := newTestScheme(t)

	// Knowledge CRD exists but is not Ready.
	knowledge := &v1alpha1.Knowledge{
		ObjectMeta: metav1.ObjectMeta{Name: "flavor-groups"},
		Spec: v1alpha1.KnowledgeSpec{
			SchedulingDomain: v1alpha1.SchedulingDomainNova,
			Extractor:        v1alpha1.KnowledgeExtractorSpec{Name: "flavor_groups"},
		},
		Status: v1alpha1.KnowledgeStatus{
			Conditions: []metav1.Condition{
				{
					Type:   v1alpha1.KnowledgeConditionReady,
					Status: metav1.ConditionFalse,
					Reason: "NotReady",
				},
			},
		},
	}

	fakeClient := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(knowledge).
		WithStatusSubresource(&v1alpha1.Knowledge{}).
		Build()

	c := newController(t, fakeClient, Config{
		SchedulerURL:      "http://localhost:9999",
		TotalPipeline:     "kvm-report-capacity",
		PlaceablePipeline: "kvm-general-purpose",
	})

	// Should return an error when knowledge is not ready.
	if err := c.reconcileAll(context.Background()); err == nil {
		t.Error("reconcileAll should fail when flavor groups knowledge is not ready")
	}
}

func TestReconcileAZ_ZeroMemoryFlavorSkipped(t *testing.T) {
	scheme := newTestScheme(t)
	fakeClient := fake.NewClientBuilder().
		WithScheme(scheme).
		WithStatusSubresource(&v1alpha1.FlavorGroupCapacity{}).
		Build()
	c := newController(t, fakeClient, Config{})

	groupData := compute.FlavorGroupFeature{
		SmallestFlavor: compute.FlavorInGroup{Name: "bad-flavor", MemoryMB: 0},
	}
	// reconcileAZ logs and skips groups with zero memory; it does not return an error.
	err := c.reconcileAZ(context.Background(), "az-a",
		map[string]compute.FlavorGroupFeature{"hana-v2": groupData},
		nil, nil, nil)
	if err != nil {
		t.Errorf("reconcileAZ should not return error for zero-memory flavor, got: %v", err)
	}

	// No CRD should have been created.
	var list v1alpha1.FlavorGroupCapacityList
	if err := fakeClient.List(context.Background(), &list); err != nil {
		t.Fatalf("failed to list CRDs: %v", err)
	}
	if len(list.Items) != 0 {
		t.Errorf("expected 0 CRDs, got %d", len(list.Items))
	}
}

func TestFlavorSlots(t *testing.T) {
	const (
		mem4GiB  = 4 * 1024 * 1024 * 1024
		mem8GiB  = 8 * 1024 * 1024 * 1024
		mem32GiB = 32 * 1024 * 1024 * 1024
	)
	tests := []struct {
		name           string
		memRemaining   int64
		coresRemaining int64
		flavorMem      int64
		flavorCPUs     int64
		want           int64
	}{
		{
			name: "memory is binding constraint",
			// 8 GiB available, flavor needs 4 GiB and 2 cores; 64 cores available → 2 mem-slots, 32 cpu-slots
			memRemaining: mem8GiB, coresRemaining: 64, flavorMem: mem4GiB, flavorCPUs: 2, want: 2,
		},
		{
			name: "CPU is binding constraint",
			// 32 GiB available (fits 8 slots), only 3 cores available (fits 1 slot at 2 vcpus)
			memRemaining: mem32GiB, coresRemaining: 3, flavorMem: mem4GiB, flavorCPUs: 2, want: 1,
		},
		{
			name:         "both constraints equal",
			memRemaining: mem8GiB, coresRemaining: 4, flavorMem: mem4GiB, flavorCPUs: 2, want: 2,
		},
		{
			name:         "zero VCPUs — CPU dimension ignored",
			memRemaining: mem8GiB, coresRemaining: 0, flavorMem: mem4GiB, flavorCPUs: 0, want: 2,
		},
		{
			name:         "not enough memory for even one slot",
			memRemaining: mem4GiB - 1, coresRemaining: 64, flavorMem: mem4GiB, flavorCPUs: 2, want: 0,
		},
		{
			name:         "not enough CPU for even one slot",
			memRemaining: mem32GiB, coresRemaining: 1, flavorMem: mem4GiB, flavorCPUs: 2, want: 0,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resources := map[string]int64{
				ResourceMemory: tt.memRemaining,
				ResourceCores:  tt.coresRemaining,
			}
			got := flavorSlots(resources, tt.flavorMem, tt.flavorCPUs)
			if got != tt.want {
				t.Errorf("flavorSlots() = %d, want %d", got, tt.want)
			}
		})
	}
}

func TestComputeTotalCapacity(t *testing.T) {
	mb := func(mb int64) int64 { return mb * 1024 * 1024 }
	tests := []struct {
		name         string
		flavors      []v1alpha1.FlavorCapacityStatus
		specs        map[string]compute.FlavorInGroup
		wantMemBytes int64
		wantCPU      int64
	}{
		{
			name: "single flavor",
			flavors: []v1alpha1.FlavorCapacityStatus{
				{FlavorName: "small", TotalCapacityVMSlots: 10},
			},
			specs: map[string]compute.FlavorInGroup{
				"small": {Name: "small", MemoryMB: 4096, VCPUs: 2},
			},
			wantMemBytes: 10 * mb(4096),
			wantCPU:      20,
		},
		{
			name: "picks flavor with most total memory, not most slots",
			// mem: large wins (2×32GiB=64GiB > 10×4GiB=40GiB); CPU: small wins (10×2=20 > 2×8=16)
			flavors: []v1alpha1.FlavorCapacityStatus{
				{FlavorName: "small", TotalCapacityVMSlots: 10},
				{FlavorName: "large", TotalCapacityVMSlots: 2},
			},
			specs: map[string]compute.FlavorInGroup{
				"small": {Name: "small", MemoryMB: 4096, VCPUs: 2},
				"large": {Name: "large", MemoryMB: 32768, VCPUs: 8},
			},
			wantMemBytes: 2 * mb(32768),
			wantCPU:      20,
		},
		{
			name: "zero slots excluded",
			flavors: []v1alpha1.FlavorCapacityStatus{
				{FlavorName: "small", TotalCapacityVMSlots: 0},
				{FlavorName: "large", TotalCapacityVMSlots: 3},
			},
			specs: map[string]compute.FlavorInGroup{
				"small": {Name: "small", MemoryMB: 4096, VCPUs: 2},
				"large": {Name: "large", MemoryMB: 8192, VCPUs: 4},
			},
			wantMemBytes: 3 * mb(8192),
			wantCPU:      12,
		},
		{
			name:         "all zero slots",
			flavors:      []v1alpha1.FlavorCapacityStatus{{FlavorName: "small", TotalCapacityVMSlots: 0}},
			specs:        map[string]compute.FlavorInGroup{"small": {MemoryMB: 4096, VCPUs: 2}},
			wantMemBytes: 0,
			wantCPU:      0,
		},
		{
			name:         "empty input",
			flavors:      nil,
			specs:        map[string]compute.FlavorInGroup{},
			wantMemBytes: 0,
			wantCPU:      0,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotMem, gotCPU := computeTotalCapacity(tt.flavors, tt.specs)
			if gotMem != tt.wantMemBytes {
				t.Errorf("maxMemBytes = %d, want %d", gotMem, tt.wantMemBytes)
			}
			if gotCPU != tt.wantCPU {
				t.Errorf("maxCPUCores = %d, want %d", gotCPU, tt.wantCPU)
			}
		})
	}
}

// Verify that the module-level log variable from reservations package doesn't
// collide with the one in this package.
func TestPackageLogVar(t *testing.T) {
	_ = reservations.NewSchedulerClient("http://localhost")
}

func TestSumCommittedCapacity(t *testing.T) {
	const (
		groupName = "hana-v2"
		az        = "qa-de-1a"
		memMB     = 4096
		memBytes  = int64(memMB) * 1024 * 1024
	)

	newCR := func(name, group, zone string, state v1alpha1.CommitmentStatus, resType v1alpha1.CommittedResourceType, amount string, acceptedAmount string) *v1alpha1.CommittedResource {
		qty := resource.MustParse(amount)
		cr := &v1alpha1.CommittedResource{
			ObjectMeta: metav1.ObjectMeta{Name: name},
			Spec: v1alpha1.CommittedResourceSpec{
				FlavorGroupName:  group,
				AvailabilityZone: zone,
				State:            state,
				ResourceType:     resType,
				Amount:           qty,
			},
		}
		if acceptedAmount != "" {
			accepted := resource.MustParse(acceptedAmount)
			cr.Status.AcceptedSpec = &v1alpha1.CommittedResourceSpec{
				Amount: accepted,
			}
		}
		return cr
	}

	scheme := newTestScheme(t)
	objects := []client.Object{
		// Should count: confirmed, memory, right group+AZ, AcceptedAmount set.
		newCR("cr1", groupName, az, v1alpha1.CommitmentStatusConfirmed, v1alpha1.CommittedResourceTypeMemory, "8Gi", "8Gi"),
		// Should count: guaranteed, memory, right group+AZ, no AcceptedAmount → falls back to Spec.Amount.
		newCR("cr2", groupName, az, v1alpha1.CommitmentStatusGuaranteed, v1alpha1.CommittedResourceTypeMemory, "4Gi", ""),
		// Should NOT count: wrong state.
		newCR("cr3", groupName, az, v1alpha1.CommitmentStatusPlanned, v1alpha1.CommittedResourceTypeMemory, "4Gi", ""),
		// Should NOT count: wrong resource type.
		newCR("cr4", groupName, az, v1alpha1.CommitmentStatusConfirmed, v1alpha1.CommittedResourceTypeCores, "4Gi", ""),
		// Should NOT count: wrong AZ.
		newCR("cr5", groupName, "other-az", v1alpha1.CommitmentStatusConfirmed, v1alpha1.CommittedResourceTypeMemory, "4Gi", ""),
		// Should NOT count: wrong flavor group.
		newCR("cr6", "other-group", az, v1alpha1.CommitmentStatusConfirmed, v1alpha1.CommittedResourceTypeMemory, "4Gi", ""),
	}

	fakeClient := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(objects...).
		Build()

	c := newController(t, fakeClient, Config{})
	// smallestFlavorBytes = 4GiB → cr1 = 8GiB/4GiB = 2 slots, cr2 = 4GiB/4GiB = 1 slot → total = 3.
	got, err := c.sumCommittedCapacity(context.Background(), groupName, az, memBytes)
	if err != nil {
		t.Fatalf("sumCommittedCapacity failed: %v", err)
	}
	if got != 3 {
		t.Errorf("sumCommittedCapacity = %d, want 3", got)
	}
}

// TestProbeScheduler_SubtractsReservationBlocksWhenNotIgnored verifies that placeable-probe
// slot counting subtracts per-host reservation blocks in addition to hv.Status.Allocation.
// mockVMSource is a test implementation of VMSource that returns a fixed list of VMs.
type mockVMSource struct {
	vms []reservations.VM
	err error
}

func (m *mockVMSource) ListVMs(_ context.Context) ([]reservations.VM, error) {
	return m.vms, m.err
}

func (m *mockVMSource) ListVMsByProject(_ context.Context, _ string) ([]reservations.VM, error) {
	return m.vms, m.err
}

func (m *mockVMSource) ListVMsOnHypervisors(_ context.Context, _ *hv1.HypervisorList, _ bool) ([]reservations.VM, error) {
	return m.vms, m.err
}

func (m *mockVMSource) GetVM(_ context.Context, _ string) (*reservations.VM, error) {
	return nil, nil
}

func (m *mockVMSource) IsServerActive(_ context.Context, _ string) (bool, error) {
	return false, nil
}

func (m *mockVMSource) GetDeletedVMInfo(_ context.Context, _ string) (*reservations.DeletedVMInfo, error) {
	return nil, nil
}

// TestComputeVMUsage_ZerosOutWhenAllVMsRemoved verifies that after a successful
// ListVMsOnHypervisors call that returns no VMs for a (flavorGroup, AZ) pair,
// the result map entry has fresh=true with zero instances/resources, ensuring
// RunningInstances and RunningResources are zeroed out in the CRD.
func TestComputeVMUsage_ZerosOutWhenAllVMsRemoved(t *testing.T) {
	const (
		groupName = "hana-v2"
		az        = "qa-de-1a"
		memMB     = 4096
		memBytes  = int64(memMB) * 1024 * 1024
	)

	scheme := newTestScheme(t)
	hv := newHypervisor("host-1", az, memBytes, "vm1")
	knowledge := newFlavorGroupKnowledge(t, groupName, memMB)

	// Pre-create CRD with non-zero RunningInstances to simulate prior state.
	crdName := crdNameFor(groupName, az)
	existing := &v1alpha1.FlavorGroupCapacity{
		ObjectMeta: metav1.ObjectMeta{Name: crdName},
		Spec: v1alpha1.FlavorGroupCapacitySpec{
			FlavorGroup:      groupName,
			AvailabilityZone: az,
		},
	}

	fakeClient := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(knowledge, hv, existing).
		WithStatusSubresource(&v1alpha1.FlavorGroupCapacity{}, &v1alpha1.Knowledge{}).
		Build()

	// Set RunningInstances to non-zero via a status patch.
	patch := client.MergeFrom(existing.DeepCopy())
	existing.Status.RunningInstances = 5
	existing.Status.RunningResources = map[string]resource.Quantity{
		string(v1alpha1.CommittedResourceTypeMemory): *resource.NewQuantity(memBytes*5, resource.BinarySI),
	}
	if err := fakeClient.Status().Patch(context.Background(), existing, patch); err != nil {
		t.Fatalf("failed to patch CRD status: %v", err)
	}

	// Scheduler returns host-1 so probes succeed.
	schedulerServer := newMockSchedulerServer(t, []string{"host-1"})
	defer schedulerServer.Close()

	// VMSource returns empty list (all VMs removed).
	vmSource := &mockVMSource{vms: []reservations.VM{}}

	ctrl := NewController(fakeClient, Config{
		SchedulerURL:      schedulerServer.URL,
		TotalPipeline:     "kvm-report-capacity",
		PlaceablePipeline: "kvm-general-purpose",
	}, vmSource)

	smallFlavor := compute.FlavorInGroup{Name: groupName + "-small", MemoryMB: memMB, VCPUs: 2}
	groupData := compute.FlavorGroupFeature{
		Name:           groupName,
		SmallestFlavor: smallFlavor,
		Flavors:        []compute.FlavorInGroup{smallFlavor},
	}
	hvByName := map[string]hv1.Hypervisor{"host-1": *hv}
	groups := map[string]compute.FlavorGroupFeature{groupName: groupData}

	// Compute VM usage — should return fresh=true with zero instances.
	usageByKey := ctrl.computeVMUsage(context.Background(), groups, []hv1.Hypervisor{*hv})
	key := vmUsageKey{group: groupName, az: az}
	usage, exists := usageByKey[key]
	if !exists {
		t.Fatalf("expected usage entry for key %v, got none", key)
	}
	if !usage.fresh {
		t.Errorf("usage.fresh = false, want true (successful call with no VMs)")
	}
	if usage.instances != 0 {
		t.Errorf("usage.instances = %d, want 0", usage.instances)
	}

	// Now run reconcileAZ to verify the CRD gets zeroed out.
	if err := ctrl.reconcileAZ(context.Background(), az, groups, hvByName, map[string]int64{}, usageByKey); err != nil {
		t.Fatalf("reconcileAZ failed: %v", err)
	}

	var crd v1alpha1.FlavorGroupCapacity
	if err := fakeClient.Get(context.Background(), types.NamespacedName{Name: crdName}, &crd); err != nil {
		t.Fatalf("failed to get CRD: %v", err)
	}
	if crd.Status.RunningInstances != 0 {
		t.Errorf("RunningInstances = %d, want 0 (all VMs removed)", crd.Status.RunningInstances)
	}
	if crd.Status.RunningResources != nil {
		for k, v := range crd.Status.RunningResources {
			if !v.IsZero() {
				t.Errorf("RunningResources[%s] = %s, want 0", k, v.String())
			}
		}
	}
}

func TestProbeScheduler_SubtractsReservationBlocksWhenNotIgnored(t *testing.T) {
	const memMB = 4096
	const memBytes = int64(memMB) * 1024 * 1024

	scheme := newTestScheme(t)

	// Host has 3-slot capacity (3 × flavor), 1 slot used by running VM, 1 slot blocked by reservation.
	hv := newHypervisor("host-1", "az-a", memBytes*3)
	hv.Status.Allocation = map[hv1.ResourceName]resource.Quantity{
		hv1.ResourceMemory: *resource.NewQuantity(memBytes, resource.BinarySI),
	}

	fakeClient := fake.NewClientBuilder().WithScheme(scheme).Build()
	srv := newMockSchedulerServer(t, []string{"host-1"})
	defer srv.Close()

	c := newController(t, fakeClient, Config{SchedulerURL: srv.URL})
	hvByName := map[string]hv1.Hypervisor{"host-1": *hv}
	flavor := compute.FlavorInGroup{Name: "test-flavor", MemoryMB: memMB}

	// Total probe: raw 3 slots, no subtraction.
	totalCap, _, _, err := c.probeScheduler(context.Background(), flavor, "az-a", "total-pipeline", hvByName, true, nil)
	if err != nil {
		t.Fatalf("probeScheduler (total) failed: %v", err)
	}
	if totalCap != 3 {
		t.Errorf("total capacity = %d, want 3", totalCap)
	}

	// Placeable probe with 1 reservation block: 3 - 1 (alloc) - 1 (reservation) = 1 slot.
	blockedByReservations := map[string]int64{
		"host-1": memBytes, // 1 reservation blocking 1 slot's worth of memory
	}
	placeableCap, _, _, err := c.probeScheduler(context.Background(), flavor, "az-a", "placeable-pipeline", hvByName, false, blockedByReservations)
	if err != nil {
		t.Fatalf("probeScheduler (placeable) failed: %v", err)
	}
	if placeableCap != 1 {
		t.Errorf("placeable capacity = %d, want 1 (3 slots − 1 alloc − 1 reservation)", placeableCap)
	}
}

func TestProbeScheduler_SetsCapacityProbeIntent(t *testing.T) {
	scheme := newTestScheme(t)
	hv := newHypervisor("host-1", "az-a", 4096*1024*1024)

	var capturedReq schedulerapi.ExternalSchedulerRequest
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(&capturedReq); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		json.NewEncoder(w).Encode(schedulerapi.ExternalSchedulerResponse{Hosts: []string{"host-1"}}) //nolint:errcheck
	}))
	defer srv.Close()

	c := NewController(fake.NewClientBuilder().WithScheme(scheme).Build(), Config{SchedulerURL: srv.URL}, nil)
	hvByName := map[string]hv1.Hypervisor{"host-1": *hv}
	flavor := compute.FlavorInGroup{Name: "test-flavor", MemoryMB: 4096}

	if _, _, _, err := c.probeScheduler(context.Background(), flavor, "az-a", "test-pipeline", hvByName, true, nil); err != nil {
		t.Fatalf("probeScheduler failed: %v", err)
	}
	hint, err := capturedReq.Spec.Data.GetSchedulerHintStr("_nova_check_type")
	if err != nil {
		t.Fatalf("failed to get _nova_check_type hint: %v", err)
	}
	if hint != string(schedulerapi.CapacityProbeIntent) {
		t.Errorf("capacity probe must set _nova_check_type=%q, got %q", schedulerapi.CapacityProbeIntent, hint)
	}
	if capturedReq.Spec.Data.ProjectID != "cortex-capacity-probe" {
		t.Errorf("capacity probe must send ProjectID cortex-capacity-probe, got %q", capturedReq.Spec.Data.ProjectID)
	}
}

// TestReconcile_ReactsToKnowledgeChange verifies that Reconcile() runs reconcileAll() and
// writes FlavorGroupCapacity CRDs when triggered by a watch event (simulated here by calling
// Reconcile directly with the coalesced key).
func TestReconcile_ReactsToKnowledgeChange(t *testing.T) {
	const (
		groupName = "hana-v2"
		az        = "qa-de-1a"
		memMB     = 4096
		memBytes  = int64(memMB) * 1024 * 1024
	)

	scheme := newTestScheme(t)
	hv := newHypervisor("host-1", az, memBytes)
	knowledge := newFlavorGroupKnowledge(t, groupName, memMB)

	fakeClient := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(knowledge, hv).
		WithStatusSubresource(&v1alpha1.FlavorGroupCapacity{}, &v1alpha1.Knowledge{}).
		Build()

	schedulerServer := newMockSchedulerServer(t, []string{"host-1"})
	defer schedulerServer.Close()

	r := NewController(fakeClient, Config{
		SchedulerURL:         schedulerServer.URL,
		TotalPipeline:        "kvm-report-capacity",
		PlaceablePipeline:    "kvm-general-purpose",
		ReconcileInterval:    metav1.Duration{Duration: 5 * time.Minute},
		MinReconcileInterval: metav1.Duration{Duration: 30 * time.Second},
	}, nil)

	req := ctrl.Request{NamespacedName: types.NamespacedName{Name: coalescedKey}}
	result, err := r.Reconcile(context.Background(), req)
	if err != nil {
		t.Fatalf("Reconcile returned error: %v", err)
	}
	if result.RequeueAfter != 5*time.Minute {
		t.Errorf("RequeueAfter = %v, want 5m (periodic floor)", result.RequeueAfter)
	}

	// reconcileAll should have written one CRD for the single (group × AZ) pair.
	var list v1alpha1.FlavorGroupCapacityList
	if err := fakeClient.List(context.Background(), &list); err != nil {
		t.Fatalf("failed to list CRDs: %v", err)
	}
	if len(list.Items) != 1 {
		t.Errorf("expected 1 FlavorGroupCapacity CRD after reactive reconcile, got %d", len(list.Items))
	}
}

// TestReconcile_MinIntervalEarlyReturn verifies that a second Reconcile() call within
// MinReconcileInterval returns early (no reconcileAll) with RequeueAfter set to the
// remaining cooldown duration.
func TestReconcile_MinIntervalEarlyReturn(t *testing.T) {
	const (
		groupName = "hana-v2"
		az        = "qa-de-1a"
		memMB     = 4096
		memBytes  = int64(memMB) * 1024 * 1024
	)

	scheme := newTestScheme(t)
	hv := newHypervisor("host-1", az, memBytes)
	knowledge := newFlavorGroupKnowledge(t, groupName, memMB)

	fakeClient := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(knowledge, hv).
		WithStatusSubresource(&v1alpha1.FlavorGroupCapacity{}, &v1alpha1.Knowledge{}).
		Build()

	schedulerServer := newMockSchedulerServer(t, []string{"host-1"})
	defer schedulerServer.Close()

	minInterval := 30 * time.Second
	r := NewController(fakeClient, Config{
		SchedulerURL:         schedulerServer.URL,
		TotalPipeline:        "kvm-report-capacity",
		PlaceablePipeline:    "kvm-general-purpose",
		ReconcileInterval:    metav1.Duration{Duration: 5 * time.Minute},
		MinReconcileInterval: metav1.Duration{Duration: minInterval},
	}, nil)

	req := ctrl.Request{NamespacedName: types.NamespacedName{Name: coalescedKey}}

	// First call: should run reconcileAll and succeed.
	if _, err := r.Reconcile(context.Background(), req); err != nil {
		t.Fatalf("first Reconcile returned error: %v", err)
	}

	// Second call immediately after: should return early without running reconcileAll.
	result, err := r.Reconcile(context.Background(), req)
	if err != nil {
		t.Fatalf("second Reconcile returned error: %v", err)
	}
	if result.RequeueAfter == 0 {
		t.Fatal("second Reconcile: expected non-zero RequeueAfter (min interval early return), got 0")
	}
	if result.RequeueAfter > minInterval {
		t.Errorf("second Reconcile: RequeueAfter %v exceeds MinReconcileInterval %v", result.RequeueAfter, minInterval)
	}

	// Only one CRD should exist — the second call must not have triggered reconcileAll.
	var list v1alpha1.FlavorGroupCapacityList
	if err := fakeClient.List(context.Background(), &list); err != nil {
		t.Fatalf("failed to list CRDs: %v", err)
	}
	if len(list.Items) != 1 {
		t.Errorf("expected 1 CRD (only first reconcile ran), got %d", len(list.Items))
	}
}
