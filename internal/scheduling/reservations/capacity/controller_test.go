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

	hv1 "github.com/cobaltcore-dev/openstack-hypervisor-operator/api/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	schedulerapi "github.com/cobaltcore-dev/cortex/api/external/nova"
	"github.com/cobaltcore-dev/cortex/api/scheduling"
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

// newHypervisor creates a Hypervisor CRD with a topology AZ label and effective capacity.
func newHypervisor(name, az string, memoryBytes int64, instanceIDs ...string) *hv1.Hypervisor {
	hv := &hv1.Hypervisor{
		ObjectMeta: metav1.ObjectMeta{
			Name:   name,
			Labels: map[string]string{"topology.kubernetes.io/zone": az},
		},
	}
	if memoryBytes > 0 {
		qty := resource.NewQuantity(memoryBytes, resource.BinarySI)
		hv.Status.EffectiveCapacity = map[hv1.ResourceName]resource.Quantity{
			hv1.ResourceMemory: *qty,
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
	hvs := []hv1.Hypervisor{
		*newHypervisor("h1", "az-a", 0, "vm1", "vm2"),
		*newHypervisor("h2", "az-a", 0, "vm3"),
		*newHypervisor("h3", "az-b", 0, "vm4"),
	}
	if got := countInstancesInAZ(hvs, "az-a"); got != 3 {
		t.Errorf("countInstancesInAZ(az-a) = %d, want 3", got)
	}
	if got := countInstancesInAZ(hvs, "az-b"); got != 1 {
		t.Errorf("countInstancesInAZ(az-b) = %d, want 1", got)
	}
	if got := countInstancesInAZ(hvs, "az-c"); got != 0 {
		t.Errorf("countInstancesInAZ(az-c) = %d, want 0", got)
	}
}

// --- integration-style tests for reconcileOne ---

func TestReconcileOne_CreatesCRD(t *testing.T) {
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

	// Both probes return host-1 so capacity = floor(4GiB/4GiB) = 1
	schedulerServer := newMockSchedulerServer(t, []string{"host-1"})
	defer schedulerServer.Close()

	ctrl := NewController(fakeClient, Config{
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

	if err := ctrl.reconcileOne(context.Background(), groupName, groupData, az, hvByName, []hv1.Hypervisor{*hv}); err != nil {
		t.Fatalf("reconcileOne failed: %v", err)
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
	if crd.Status.TotalInstances != 1 {
		t.Errorf("TotalInstances = %d, want 1", crd.Status.TotalInstances)
	}
}

func TestReconcileOne_SetsReadyConditionFalseOnSchedulerError(t *testing.T) {
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

	// Scheduler returns 500 to simulate error
	failServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer failServer.Close()

	ctrl := NewController(fakeClient, Config{
		SchedulerURL:      failServer.URL,
		TotalPipeline:     "kvm-report-capacity",
		PlaceablePipeline: "kvm-general-purpose",
	})

	smallFlavor := compute.FlavorInGroup{Name: groupName + "-small", MemoryMB: memMB, VCPUs: 2}
	groupData := compute.FlavorGroupFeature{
		SmallestFlavor: smallFlavor,
		Flavors:        []compute.FlavorInGroup{smallFlavor},
	}

	// reconcileOne returns no error itself (it continues on probe failure), but sets Ready=False
	if err := ctrl.reconcileOne(context.Background(), groupName, groupData, az, map[string]hv1.Hypervisor{}, []hv1.Hypervisor{}); err != nil {
		t.Fatalf("reconcileOne failed: %v", err)
	}

	var crd v1alpha1.FlavorGroupCapacity
	if err := fakeClient.Get(context.Background(), types.NamespacedName{Name: crdNameFor(groupName, az)}, &crd); err != nil {
		t.Fatalf("failed to get CRD: %v", err)
	}

	var freshStatus metav1.ConditionStatus
	for _, c := range crd.Status.Conditions {
		if c.Type == v1alpha1.FlavorGroupCapacityConditionReady {
			freshStatus = c.Status
		}
	}
	if freshStatus != metav1.ConditionFalse {
		t.Errorf("Ready condition = %q, want %q", freshStatus, metav1.ConditionFalse)
	}
}

func TestReconcileOne_IdempotentUpdate(t *testing.T) {
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

	// Pre-create the CRD to test the update path (not create path)
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

	ctrl := NewController(fakeClient, Config{
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

	// First call
	if err := ctrl.reconcileOne(context.Background(), groupName, groupData, az, hvByName, []hv1.Hypervisor{*hv}); err != nil {
		t.Fatalf("first reconcileOne failed: %v", err)
	}
	// Second call — should not error on the already-existing CRD
	if err := ctrl.reconcileOne(context.Background(), groupName, groupData, az, hvByName, []hv1.Hypervisor{*hv}); err != nil {
		t.Fatalf("second reconcileOne failed: %v", err)
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

	// No hypervisors → no AZs → reconcileAll returns without error
	fakeClient := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(knowledge).
		WithStatusSubresource(&v1alpha1.FlavorGroupCapacity{}, &v1alpha1.Knowledge{}).
		Build()

	ctrl := NewController(fakeClient, Config{
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

	// Scheduler returns both hosts
	srv := newMockSchedulerServer(t, []string{"host-1", "host-2"})
	defer srv.Close()

	c := NewController(fakeClient, Config{SchedulerURL: srv.URL})
	hvByName := map[string]hv1.Hypervisor{
		"host-1": *hv1Obj,
		"host-2": *hv2Obj,
	}
	flavor := compute.FlavorInGroup{Name: "test-flavor", MemoryMB: memMB}

	capacity, hosts, err := c.probeScheduler(context.Background(), flavor, "az-a", "test-pipeline", hvByName, scheduling.Options{SkipHistory: true})
	if err != nil {
		t.Fatalf("probeScheduler failed: %v", err)
	}
	if hosts != 2 {
		t.Errorf("hosts = %d, want 2", hosts)
	}
	// host-1 = 1 slot (4GiB/4GiB), host-2 = 2 slots (8GiB/4GiB)
	if capacity != 3 {
		t.Errorf("capacity = %d, want 3", capacity)
	}
}

func TestReconcileAll_MultipleGroupsAndAZs(t *testing.T) {
	scheme := newTestScheme(t)

	const memMB = 2048
	const memBytes = int64(memMB) * 1024 * 1024

	// Two AZs, two hypervisors
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

	c := NewController(fakeClient, Config{
		SchedulerURL:      srv.URL,
		TotalPipeline:     "kvm-report-capacity",
		PlaceablePipeline: "kvm-general-purpose",
	})

	if err := c.reconcileAll(context.Background()); err != nil {
		t.Fatalf("reconcileAll failed: %v", err)
	}

	// Expect one CRD per AZ for the single group
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

	// Knowledge CRD exists but is not Ready
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

	c := NewController(fakeClient, Config{
		SchedulerURL:      "http://localhost:9999",
		TotalPipeline:     "kvm-report-capacity",
		PlaceablePipeline: "kvm-general-purpose",
	})

	// Should return an error when knowledge is not ready
	if err := c.reconcileAll(context.Background()); err == nil {
		t.Error("reconcileAll should fail when flavor groups knowledge is not ready")
	}
}

func TestReconcileOne_ZeroMemoryFlavorReturnsError(t *testing.T) {
	scheme := newTestScheme(t)
	fakeClient := fake.NewClientBuilder().WithScheme(scheme).Build()
	c := NewController(fakeClient, Config{})

	groupData := compute.FlavorGroupFeature{
		SmallestFlavor: compute.FlavorInGroup{Name: "bad-flavor", MemoryMB: 0},
	}
	err := c.reconcileOne(context.Background(), "hana-v2", groupData, "az-a", nil, nil)
	if err == nil {
		t.Error("expected error for zero-memory flavor")
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
		// Should count: confirmed, memory, right group+AZ, AcceptedAmount set
		newCR("cr1", groupName, az, v1alpha1.CommitmentStatusConfirmed, v1alpha1.CommittedResourceTypeMemory, "8Gi", "8Gi"),
		// Should count: guaranteed, memory, right group+AZ, no AcceptedAmount → falls back to Spec.Amount
		newCR("cr2", groupName, az, v1alpha1.CommitmentStatusGuaranteed, v1alpha1.CommittedResourceTypeMemory, "4Gi", ""),
		// Should NOT count: wrong state
		newCR("cr3", groupName, az, v1alpha1.CommitmentStatusPlanned, v1alpha1.CommittedResourceTypeMemory, "4Gi", ""),
		// Should NOT count: wrong resource type
		newCR("cr4", groupName, az, v1alpha1.CommitmentStatusConfirmed, v1alpha1.CommittedResourceTypeCores, "4Gi", ""),
		// Should NOT count: wrong AZ
		newCR("cr5", groupName, "other-az", v1alpha1.CommitmentStatusConfirmed, v1alpha1.CommittedResourceTypeMemory, "4Gi", ""),
		// Should NOT count: wrong flavor group
		newCR("cr6", "other-group", az, v1alpha1.CommitmentStatusConfirmed, v1alpha1.CommittedResourceTypeMemory, "4Gi", ""),
	}

	fakeClient := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(objects...).
		Build()

	c := NewController(fakeClient, Config{})
	// smallestFlavorBytes = 4GiB → cr1 = 8GiB/4GiB = 2 slots, cr2 = 4GiB/4GiB = 1 slot → total = 3
	got, err := c.sumCommittedCapacity(context.Background(), groupName, az, memBytes)
	if err != nil {
		t.Fatalf("sumCommittedCapacity failed: %v", err)
	}
	if got != 3 {
		t.Errorf("sumCommittedCapacity = %d, want 3", got)
	}
}
