// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package placement

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	hv1 "github.com/cobaltcore-dev/openstack-hypervisor-operator/api/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

// ---------------------------------------------------------------------------
// Test helper factories
// ---------------------------------------------------------------------------

func testScheme(t *testing.T) *runtime.Scheme {
	t.Helper()
	s := runtime.NewScheme()
	if err := hv1.AddToScheme(s); err != nil {
		t.Fatalf("hv1 scheme: %v", err)
	}
	return s
}

func testHypervisor(name, openstackID string) hv1.Hypervisor {
	return hv1.Hypervisor{
		ObjectMeta: metav1.ObjectMeta{Name: name},
		Status:     hv1.HypervisorStatus{HypervisorID: openstackID},
	}
}

func testHypervisorFull(
	name, openstackID string,
	aggregates []hv1.Aggregate,
	traits []string,
	capacity map[hv1.ResourceName]resource.Quantity,
) hv1.Hypervisor {

	return hv1.Hypervisor{
		ObjectMeta: metav1.ObjectMeta{Name: name},
		Status: hv1.HypervisorStatus{
			HypervisorID:      openstackID,
			Aggregates:        aggregates,
			Traits:            traits,
			EffectiveCapacity: capacity,
		},
	}
}

func newFakeClient(t *testing.T, objs ...client.Object) client.Client {
	t.Helper()
	s := testScheme(t)
	builder := fake.NewClientBuilder().WithScheme(s)
	if len(objs) > 0 {
		builder = builder.WithObjects(objs...)
	}
	builder = builder.WithIndex(&hv1.Hypervisor{}, idxHypervisorOpenStackId, func(obj client.Object) []string {
		hv, ok := obj.(*hv1.Hypervisor)
		if !ok {
			return nil
		}
		if hv.Status.HypervisorID == "" {
			return nil
		}
		return []string{hv.Status.HypervisorID}
	})
	builder = builder.WithIndex(&hv1.Hypervisor{}, idxHypervisorName, func(obj client.Object) []string {
		hv, ok := obj.(*hv1.Hypervisor)
		if !ok {
			return nil
		}
		return []string{hv.Name}
	})
	return builder.Build()
}

func newTestShimWithHypervisors(t *testing.T, upstreamStatus int, upstreamBody string, hvs ...client.Object) *Shim {
	t.Helper()
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(upstreamStatus)
		if _, err := w.Write([]byte(upstreamBody)); err != nil {
			t.Errorf("failed to write upstream body: %v", err)
		}
	}))
	t.Cleanup(upstream.Close)
	down, up := newTestTimers()
	return &Shim{
		Client: newFakeClient(t, hvs...),
		config: config{
			PlacementURL: upstream.URL,
			Features:     featuresConfig{EnableResourceProviders: true},
		},
		httpClient:             upstream.Client(),
		maxBodyLogSize:         4096,
		downstreamRequestTimer: down,
		upstreamRequestTimer:   up,
	}
}

// ---------------------------------------------------------------------------
// Filter unit tests
// ---------------------------------------------------------------------------

func TestFilterHypervisorsByUUID(t *testing.T) {
	ctx := context.Background()
	hvs := []hv1.Hypervisor{
		testHypervisor("hv1", "uuid-1"),
		testHypervisor("hv2", "uuid-2"),
		testHypervisor("hv3", "uuid-3"),
	}
	t.Run("match one", func(t *testing.T) {
		got := filterHypervisorsByUUID(ctx, hvs, "uuid-2")
		if len(got) != 1 || got[0].Name != "hv2" {
			t.Errorf("got %v, want [hv2]", names(got))
		}
	})
	t.Run("no match", func(t *testing.T) {
		got := filterHypervisorsByUUID(ctx, hvs, "uuid-999")
		if len(got) != 0 {
			t.Errorf("got %v, want empty", names(got))
		}
	})
	t.Run("empty input", func(t *testing.T) {
		got := filterHypervisorsByUUID(ctx, nil, "uuid-1")
		if len(got) != 0 {
			t.Errorf("got %v, want empty", names(got))
		}
	})
}

func TestFilterHypervisorsByName(t *testing.T) {
	ctx := context.Background()
	hvs := []hv1.Hypervisor{
		testHypervisor("node-01", "id-1"),
		testHypervisor("node-02", "id-2"),
	}
	t.Run("match", func(t *testing.T) {
		got := filterHypervisorsByName(ctx, hvs, "node-01")
		if len(got) != 1 || got[0].Name != "node-01" {
			t.Errorf("got %v, want [node-01]", names(got))
		}
	})
	t.Run("no match", func(t *testing.T) {
		got := filterHypervisorsByName(ctx, hvs, "node-99")
		if len(got) != 0 {
			t.Errorf("got %v, want empty", names(got))
		}
	})
}

func TestFilterHypervisorsByMemberOf(t *testing.T) {
	ctx := context.Background()
	agg1 := hv1.Aggregate{Name: "az1", UUID: "agg-uuid-1"}
	agg2 := hv1.Aggregate{Name: "az2", UUID: "agg-uuid-2"}
	hvs := []hv1.Hypervisor{
		testHypervisorFull("hv1", "id-1", []hv1.Aggregate{agg1}, nil, nil),
		testHypervisorFull("hv2", "id-2", []hv1.Aggregate{agg1, agg2}, nil, nil),
		testHypervisorFull("hv3", "id-3", []hv1.Aggregate{agg2}, nil, nil),
		testHypervisorFull("hv4", "id-4", nil, nil, nil),
	}

	t.Run("bare UUID match", func(t *testing.T) {
		got := filterHypervisorsByMemberOf(ctx, hvs, []string{"agg-uuid-1"})
		if len(got) != 2 {
			t.Fatalf("got %v, want [hv1, hv2]", names(got))
		}
	})
	t.Run("in: any-of", func(t *testing.T) {
		got := filterHypervisorsByMemberOf(ctx, hvs, []string{"in:agg-uuid-1,agg-uuid-2"})
		if len(got) != 3 {
			t.Fatalf("got %v, want [hv1, hv2, hv3]", names(got))
		}
	})
	t.Run("forbidden", func(t *testing.T) {
		got := filterHypervisorsByMemberOf(ctx, hvs, []string{"!agg-uuid-1"})
		if len(got) != 2 {
			t.Fatalf("got %v, want [hv3, hv4]", names(got))
		}
	})
	t.Run("forbidden in:", func(t *testing.T) {
		got := filterHypervisorsByMemberOf(ctx, hvs, []string{"!in:agg-uuid-1,agg-uuid-2"})
		if len(got) != 1 || got[0].Name != "hv4" {
			t.Fatalf("got %v, want [hv4]", names(got))
		}
	})
	t.Run("AND across repeated params", func(t *testing.T) {
		got := filterHypervisorsByMemberOf(ctx, hvs, []string{"agg-uuid-1", "agg-uuid-2"})
		if len(got) != 1 || got[0].Name != "hv2" {
			t.Fatalf("got %v, want [hv2]", names(got))
		}
	})
	t.Run("no match", func(t *testing.T) {
		got := filterHypervisorsByMemberOf(ctx, hvs, []string{"nonexistent-uuid"})
		if len(got) != 0 {
			t.Fatalf("got %v, want empty", names(got))
		}
	})
}

func TestFilterHypervisorsByInTree(t *testing.T) {
	ctx := context.Background()
	hvs := []hv1.Hypervisor{
		testHypervisor("hv1", "uuid-1"),
		testHypervisor("hv2", "uuid-2"),
	}
	t.Run("match", func(t *testing.T) {
		got := filterHypervisorsByInTree(ctx, hvs, "uuid-1")
		if len(got) != 1 || got[0].Name != "hv1" {
			t.Errorf("got %v, want [hv1]", names(got))
		}
	})
	t.Run("no match", func(t *testing.T) {
		got := filterHypervisorsByInTree(ctx, hvs, "uuid-999")
		if len(got) != 0 {
			t.Errorf("got %v, want empty", names(got))
		}
	})
}

func TestFilterHypervisorsByRequired(t *testing.T) {
	ctx := context.Background()
	hvs := []hv1.Hypervisor{
		testHypervisorFull("hv1", "id-1", nil, []string{"CUSTOM_A", "CUSTOM_B"}, nil),
		testHypervisorFull("hv2", "id-2", nil, []string{"CUSTOM_A"}, nil),
		testHypervisorFull("hv3", "id-3", nil, []string{"CUSTOM_C"}, nil),
		testHypervisorFull("hv4", "id-4", nil, nil, nil),
	}

	t.Run("single required trait", func(t *testing.T) {
		got := filterHypervisorsByRequired(ctx, hvs, []string{"CUSTOM_A"})
		if len(got) != 2 {
			t.Fatalf("got %v, want [hv1, hv2]", names(got))
		}
	})
	t.Run("multiple required traits (AND)", func(t *testing.T) {
		got := filterHypervisorsByRequired(ctx, hvs, []string{"CUSTOM_A,CUSTOM_B"})
		if len(got) != 1 || got[0].Name != "hv1" {
			t.Fatalf("got %v, want [hv1]", names(got))
		}
	})
	t.Run("forbidden trait", func(t *testing.T) {
		got := filterHypervisorsByRequired(ctx, hvs, []string{"!CUSTOM_A"})
		if len(got) != 2 {
			t.Fatalf("got %v, want [hv3, hv4]", names(got))
		}
	})
	t.Run("any-of (in:)", func(t *testing.T) {
		got := filterHypervisorsByRequired(ctx, hvs, []string{"in:CUSTOM_B,CUSTOM_C"})
		if len(got) != 2 {
			t.Fatalf("got %v, want [hv1, hv3]", names(got))
		}
	})
	t.Run("AND across repeated required params", func(t *testing.T) {
		got := filterHypervisorsByRequired(ctx, hvs, []string{"CUSTOM_A", "CUSTOM_B"})
		if len(got) != 1 || got[0].Name != "hv1" {
			t.Fatalf("got %v, want [hv1]", names(got))
		}
	})
	t.Run("no match", func(t *testing.T) {
		got := filterHypervisorsByRequired(ctx, hvs, []string{"CUSTOM_Z"})
		if len(got) != 0 {
			t.Fatalf("got %v, want empty", names(got))
		}
	})
}

func TestMatchesTraitExpr(t *testing.T) {
	traits := map[string]struct{}{
		"CUSTOM_A": {},
		"CUSTOM_B": {},
	}
	tests := []struct {
		name  string
		parts []string
		want  bool
	}{
		{"required present", []string{"CUSTOM_A"}, true},
		{"required absent", []string{"CUSTOM_Z"}, false},
		{"forbidden present", []string{"!CUSTOM_A"}, false},
		{"forbidden absent", []string{"!CUSTOM_Z"}, true},
		{"any-of hit", []string{"in:CUSTOM_Z", "CUSTOM_A"}, true},
		{"any-of miss", []string{"in:CUSTOM_X", "CUSTOM_Z"}, false},
		{"mixed pass", []string{"CUSTOM_A", "!CUSTOM_Z"}, true},
		{"mixed fail on forbidden", []string{"CUSTOM_A", "!CUSTOM_B"}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := matchesTraitExpr(traits, tt.parts); got != tt.want {
				t.Errorf("matchesTraitExpr(%v) = %v, want %v", tt.parts, got, tt.want)
			}
		})
	}
}

func TestFilterHypervisorsByResources(t *testing.T) {
	ctx := context.Background()
	cpu16 := resource.MustParse("16")
	mem64Gi := resource.MustParse("64Gi")
	hvs := []hv1.Hypervisor{
		testHypervisorFull("big", "id-1", nil, nil, map[hv1.ResourceName]resource.Quantity{
			hv1.ResourceCPU:    cpu16,
			hv1.ResourceMemory: mem64Gi,
		}),
		testHypervisorFull("small", "id-2", nil, nil, map[hv1.ResourceName]resource.Quantity{
			hv1.ResourceCPU:    resource.MustParse("2"),
			hv1.ResourceMemory: resource.MustParse("4Gi"),
		}),
		testHypervisorFull("empty", "id-3", nil, nil, nil),
	}

	t.Run("VCPU filter matches big", func(t *testing.T) {
		got, err := filterHypervisorsByResources(ctx, hvs, "VCPU:4")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(got) != 1 || got[0].Name != "big" {
			t.Errorf("got %v, want [big]", names(got))
		}
	})
	t.Run("MEMORY_MB filter", func(t *testing.T) {
		// 64 GiB = 65536 MiB
		got, err := filterHypervisorsByResources(ctx, hvs, "MEMORY_MB:65536")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(got) != 1 || got[0].Name != "big" {
			t.Errorf("got %v, want [big]", names(got))
		}
	})
	t.Run("combined VCPU and MEMORY_MB", func(t *testing.T) {
		got, err := filterHypervisorsByResources(ctx, hvs, "VCPU:2,MEMORY_MB:4096")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(got) != 2 {
			t.Errorf("got %v, want [big, small]", names(got))
		}
	})
	t.Run("DISK_GB:0 matches all", func(t *testing.T) {
		got, err := filterHypervisorsByResources(ctx, hvs, "DISK_GB:0")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(got) != len(hvs) {
			t.Errorf("got %d, want %d", len(got), len(hvs))
		}
	})
	t.Run("DISK_GB:1 matches none", func(t *testing.T) {
		got, err := filterHypervisorsByResources(ctx, hvs, "DISK_GB:1")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(got) != 0 {
			t.Errorf("got %v, want empty", names(got))
		}
	})
	t.Run("invalid format", func(t *testing.T) {
		_, err := filterHypervisorsByResources(ctx, hvs, "VCPU")
		if err == nil {
			t.Fatal("expected error for missing colon")
		}
	})
	t.Run("non-numeric amount", func(t *testing.T) {
		_, err := filterHypervisorsByResources(ctx, hvs, "VCPU:abc")
		if err == nil {
			t.Fatal("expected error for non-numeric amount")
		}
	})
	t.Run("unknown resource class", func(t *testing.T) {
		_, err := filterHypervisorsByResources(ctx, hvs, "CUSTOM_WIDGETS:5")
		if err == nil {
			t.Fatal("expected error for unknown resource class")
		}
	})
	t.Run("fallback to Capacity when EffectiveCapacity missing", func(t *testing.T) {
		hv := hv1.Hypervisor{
			ObjectMeta: metav1.ObjectMeta{Name: "cap-only"},
			Status: hv1.HypervisorStatus{
				HypervisorID: "id-cap",
				Capacity: map[hv1.ResourceName]resource.Quantity{
					hv1.ResourceCPU: resource.MustParse("8"),
				},
			},
		}
		got, err := filterHypervisorsByResources(ctx, []hv1.Hypervisor{hv}, "VCPU:4")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(got) != 1 {
			t.Errorf("got %v, want [cap-only]", names(got))
		}
	})
}

// ---------------------------------------------------------------------------
// translateToResourceProvider
// ---------------------------------------------------------------------------

func TestTranslateToResourceProvider(t *testing.T) {
	hv := hv1.Hypervisor{
		ObjectMeta: metav1.ObjectMeta{Name: "hv-node-01"},
		Status: hv1.HypervisorStatus{
			HypervisorID: validUUID,
		},
	}
	hv.Generation = 3
	rp := translateToResourceProvider(hv)

	if rp.Name != "hv-node-01" {
		t.Errorf("Name = %q, want %q", rp.Name, "hv-node-01")
	}
	if rp.UUID != validUUID {
		t.Errorf("UUID = %q, want %q", rp.UUID, validUUID)
	}
	if rp.Generation != 3 {
		t.Errorf("Generation = %d, want 3", rp.Generation)
	}
	if rp.ParentProviderUUID != nil {
		t.Errorf("ParentProviderUUID = %v, want nil (root provider)", rp.ParentProviderUUID)
	}
	if rp.RootProviderUUID == nil || *rp.RootProviderUUID != validUUID {
		t.Errorf("RootProviderUUID = %v, want %q", rp.RootProviderUUID, validUUID)
	}
	wantRels := []string{"self", "aggregates", "inventories", "allocations", "traits", "usages"}
	if len(rp.Links) != len(wantRels) {
		t.Fatalf("Links count = %d, want %d", len(rp.Links), len(wantRels))
	}
	for i, rel := range wantRels {
		if rp.Links[i].Rel != rel {
			t.Errorf("Links[%d].Rel = %q, want %q", i, rp.Links[i].Rel, rel)
		}
		if !strings.Contains(rp.Links[i].Href, validUUID) {
			t.Errorf("Links[%d].Href = %q, missing UUID", i, rp.Links[i].Href)
		}
	}
}

// ---------------------------------------------------------------------------
// Handler integration tests
// ---------------------------------------------------------------------------

func TestHandleListResourceProviders(t *testing.T) {
	hv1Obj := &hv1.Hypervisor{
		ObjectMeta: metav1.ObjectMeta{Name: "hv-node-01"},
		Status:     hv1.HypervisorStatus{HypervisorID: validUUID},
	}

	t.Run("merges upstream and k8s providers", func(t *testing.T) {
		upstreamBody := `{"resource_providers":[{"uuid":"upstream-uuid","name":"upstream-rp","generation":1,"links":[]}]}`
		s := newTestShimWithHypervisors(t, http.StatusOK, upstreamBody, hv1Obj)
		w := serveHandler(t, http.MethodGet, "/resource_providers",
			s.HandleListResourceProviders, "/resource_providers")
		if w.Code != http.StatusOK {
			t.Fatalf("status = %d, want %d", w.Code, http.StatusOK)
		}
		var resp listResourceProvidersResponse
		if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
			t.Fatalf("failed to decode response: %v", err)
		}
		if len(resp.ResourceProviders) != 2 {
			t.Fatalf("got %d providers, want 2", len(resp.ResourceProviders))
		}
	})

	t.Run("k8s wins on UUID collision", func(t *testing.T) {
		upstreamBody := `{"resource_providers":[{"uuid":"` + validUUID + `","name":"upstream-name","generation":1,"links":[]}]}`
		s := newTestShimWithHypervisors(t, http.StatusOK, upstreamBody, hv1Obj)
		w := serveHandler(t, http.MethodGet, "/resource_providers",
			s.HandleListResourceProviders, "/resource_providers")
		var resp listResourceProvidersResponse
		if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
			t.Fatalf("failed to decode response: %v", err)
		}
		if len(resp.ResourceProviders) != 1 {
			t.Fatalf("got %d providers, want 1", len(resp.ResourceProviders))
		}
		if resp.ResourceProviders[0].Name != "hv-node-01" {
			t.Errorf("name = %q, want %q", resp.ResourceProviders[0].Name, "hv-node-01")
		}
	})

	t.Run("k8s wins on name collision", func(t *testing.T) {
		upstreamBody := `{"resource_providers":[{"uuid":"other-uuid","name":"hv-node-01","generation":1,"links":[]}]}`
		s := newTestShimWithHypervisors(t, http.StatusOK, upstreamBody, hv1Obj)
		w := serveHandler(t, http.MethodGet, "/resource_providers",
			s.HandleListResourceProviders, "/resource_providers")
		var resp listResourceProvidersResponse
		if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
			t.Fatalf("failed to decode response: %v", err)
		}
		if len(resp.ResourceProviders) != 1 {
			t.Fatalf("got %d providers, want 1", len(resp.ResourceProviders))
		}
		if resp.ResourceProviders[0].UUID != validUUID {
			t.Errorf("uuid = %q, want %q", resp.ResourceProviders[0].UUID, validUUID)
		}
	})

	t.Run("upstream non-200 is forwarded", func(t *testing.T) {
		s := newTestShimWithHypervisors(t, http.StatusServiceUnavailable, "service down", hv1Obj)
		w := serveHandler(t, http.MethodGet, "/resource_providers",
			s.HandleListResourceProviders, "/resource_providers")
		if w.Code != http.StatusServiceUnavailable {
			t.Fatalf("status = %d, want %d", w.Code, http.StatusServiceUnavailable)
		}
	})

	t.Run("empty k8s list returns only upstream", func(t *testing.T) {
		upstreamBody := `{"resource_providers":[{"uuid":"u1","name":"n1","generation":0,"links":[]}]}`
		s := newTestShimWithHypervisors(t, http.StatusOK, upstreamBody)
		w := serveHandler(t, http.MethodGet, "/resource_providers",
			s.HandleListResourceProviders, "/resource_providers")
		var resp listResourceProvidersResponse
		if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
			t.Fatalf("failed to decode response: %v", err)
		}
		if len(resp.ResourceProviders) != 1 {
			t.Fatalf("got %d providers, want 1", len(resp.ResourceProviders))
		}
	})

	t.Run("hypervisors with empty HypervisorID are excluded", func(t *testing.T) {
		hvWithID := &hv1.Hypervisor{
			ObjectMeta: metav1.ObjectMeta{Name: "hv-with-id"},
			Status:     hv1.HypervisorStatus{HypervisorID: validUUID},
		}
		hvWithoutID := &hv1.Hypervisor{
			ObjectMeta: metav1.ObjectMeta{Name: "hv-without-id"},
			Status:     hv1.HypervisorStatus{HypervisorID: ""},
		}
		upstreamBody := `{"resource_providers":[]}`
		s := newTestShimWithHypervisors(t, http.StatusOK, upstreamBody, hvWithID, hvWithoutID)
		w := serveHandler(t, http.MethodGet, "/resource_providers",
			s.HandleListResourceProviders, "/resource_providers")
		if w.Code != http.StatusOK {
			t.Fatalf("status = %d, want %d", w.Code, http.StatusOK)
		}
		var resp listResourceProvidersResponse
		if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
			t.Fatalf("failed to decode response: %v", err)
		}
		if len(resp.ResourceProviders) != 1 {
			t.Fatalf("got %d providers, want 1 (only hv-with-id)", len(resp.ResourceProviders))
		}
		if resp.ResourceProviders[0].Name != "hv-with-id" {
			t.Errorf("name = %q, want %q", resp.ResourceProviders[0].Name, "hv-with-id")
		}
	})
}

func TestHandleListResourceProviders_Filters(t *testing.T) {
	agg := hv1.Aggregate{Name: "az1", UUID: "agg-uuid-1"}
	cpu16 := resource.MustParse("16")
	mem64Gi := resource.MustParse("64Gi")

	hv1Obj := &hv1.Hypervisor{
		ObjectMeta: metav1.ObjectMeta{Name: "hv-a"},
		Status: hv1.HypervisorStatus{
			HypervisorID: "aaaa-aaaa",
			Aggregates:   []hv1.Aggregate{agg},
			Traits:       []string{"CUSTOM_TRAIT_A"},
			EffectiveCapacity: map[hv1.ResourceName]resource.Quantity{
				hv1.ResourceCPU:    cpu16,
				hv1.ResourceMemory: mem64Gi,
			},
		},
	}
	hv2Obj := &hv1.Hypervisor{
		ObjectMeta: metav1.ObjectMeta{Name: "hv-b"},
		Status: hv1.HypervisorStatus{
			HypervisorID: "bbbb-bbbb",
			Traits:       []string{"CUSTOM_TRAIT_B"},
		},
	}

	emptyUpstream := `{"resource_providers":[]}`

	t.Run("filter by uuid", func(t *testing.T) {
		s := newTestShimWithHypervisors(t, http.StatusOK, emptyUpstream, hv1Obj, hv2Obj)
		w := serveHandler(t, http.MethodGet, "/resource_providers",
			s.HandleListResourceProviders, "/resource_providers?uuid=aaaa-aaaa")
		var resp listResourceProvidersResponse
		if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
			t.Fatalf("failed to decode: %v", err)
		}
		if len(resp.ResourceProviders) != 1 || resp.ResourceProviders[0].Name != "hv-a" {
			t.Errorf("got %v, want [hv-a]", resp.ResourceProviders)
		}
	})

	t.Run("filter by name", func(t *testing.T) {
		s := newTestShimWithHypervisors(t, http.StatusOK, emptyUpstream, hv1Obj, hv2Obj)
		w := serveHandler(t, http.MethodGet, "/resource_providers",
			s.HandleListResourceProviders, "/resource_providers?name=hv-b")
		var resp listResourceProvidersResponse
		if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
			t.Fatalf("failed to decode: %v", err)
		}
		if len(resp.ResourceProviders) != 1 || resp.ResourceProviders[0].Name != "hv-b" {
			t.Errorf("got %v, want [hv-b]", resp.ResourceProviders)
		}
	})

	t.Run("filter by member_of", func(t *testing.T) {
		s := newTestShimWithHypervisors(t, http.StatusOK, emptyUpstream, hv1Obj, hv2Obj)
		w := serveHandler(t, http.MethodGet, "/resource_providers",
			s.HandleListResourceProviders, "/resource_providers?member_of=agg-uuid-1")
		var resp listResourceProvidersResponse
		if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
			t.Fatalf("failed to decode: %v", err)
		}
		if len(resp.ResourceProviders) != 1 || resp.ResourceProviders[0].Name != "hv-a" {
			t.Errorf("got %v, want [hv-a]", resp.ResourceProviders)
		}
	})

	t.Run("filter by in_tree", func(t *testing.T) {
		s := newTestShimWithHypervisors(t, http.StatusOK, emptyUpstream, hv1Obj, hv2Obj)
		w := serveHandler(t, http.MethodGet, "/resource_providers",
			s.HandleListResourceProviders, "/resource_providers?in_tree=bbbb-bbbb")
		var resp listResourceProvidersResponse
		if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
			t.Fatalf("failed to decode: %v", err)
		}
		if len(resp.ResourceProviders) != 1 || resp.ResourceProviders[0].Name != "hv-b" {
			t.Errorf("got %v, want [hv-b]", resp.ResourceProviders)
		}
	})

	t.Run("filter by required", func(t *testing.T) {
		s := newTestShimWithHypervisors(t, http.StatusOK, emptyUpstream, hv1Obj, hv2Obj)
		w := serveHandler(t, http.MethodGet, "/resource_providers",
			s.HandleListResourceProviders, "/resource_providers?required=CUSTOM_TRAIT_A")
		var resp listResourceProvidersResponse
		if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
			t.Fatalf("failed to decode: %v", err)
		}
		if len(resp.ResourceProviders) != 1 || resp.ResourceProviders[0].Name != "hv-a" {
			t.Errorf("got %v, want [hv-a]", resp.ResourceProviders)
		}
	})

	t.Run("filter by resources", func(t *testing.T) {
		s := newTestShimWithHypervisors(t, http.StatusOK, emptyUpstream, hv1Obj, hv2Obj)
		w := serveHandler(t, http.MethodGet, "/resource_providers",
			s.HandleListResourceProviders, "/resource_providers?resources=VCPU:8")
		var resp listResourceProvidersResponse
		if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
			t.Fatalf("failed to decode: %v", err)
		}
		if len(resp.ResourceProviders) != 1 || resp.ResourceProviders[0].Name != "hv-a" {
			t.Errorf("got %v, want [hv-a]", resp.ResourceProviders)
		}
	})

	t.Run("invalid resources returns 400", func(t *testing.T) {
		s := newTestShimWithHypervisors(t, http.StatusOK, emptyUpstream, hv1Obj)
		w := serveHandler(t, http.MethodGet, "/resource_providers",
			s.HandleListResourceProviders, "/resource_providers?resources=INVALID")
		if w.Code != http.StatusBadRequest {
			t.Fatalf("status = %d, want %d", w.Code, http.StatusBadRequest)
		}
	})
}

func TestHandleCreateResourceProvider(t *testing.T) {
	hv1Obj := &hv1.Hypervisor{
		ObjectMeta: metav1.ObjectMeta{Name: "existing-hv"},
		Status:     hv1.HypervisorStatus{HypervisorID: validUUID},
	}

	t.Run("conflict with existing hypervisor", func(t *testing.T) {
		s := newTestShimWithHypervisors(t, http.StatusCreated, `{}`, hv1Obj)
		body := `{"name":"existing-hv"}`
		req := httptest.NewRequest(http.MethodPost, "/resource_providers", strings.NewReader(body))
		w := httptest.NewRecorder()
		s.HandleCreateResourceProvider(w, req)
		if w.Code != http.StatusConflict {
			t.Fatalf("status = %d, want %d", w.Code, http.StatusConflict)
		}
	})

	t.Run("no conflict forwards to upstream", func(t *testing.T) {
		s := newTestShimWithHypervisors(t, http.StatusCreated, `{"uuid":"new-uuid","name":"new-rp"}`, hv1Obj)
		body := `{"name":"new-rp"}`
		req := httptest.NewRequest(http.MethodPost, "/resource_providers", strings.NewReader(body))
		w := httptest.NewRecorder()
		s.HandleCreateResourceProvider(w, req)
		if w.Code != http.StatusCreated {
			t.Fatalf("status = %d, want %d", w.Code, http.StatusCreated)
		}
	})

	t.Run("uuid conflict with existing hypervisor", func(t *testing.T) {
		s := newTestShimWithHypervisors(t, http.StatusCreated, `{}`, hv1Obj)
		body := `{"name":"different-name","uuid":"` + validUUID + `"}`
		req := httptest.NewRequest(http.MethodPost, "/resource_providers", strings.NewReader(body))
		w := httptest.NewRecorder()
		s.HandleCreateResourceProvider(w, req)
		if w.Code != http.StatusConflict {
			t.Fatalf("status = %d, want %d", w.Code, http.StatusConflict)
		}
	})

	t.Run("missing name returns 400", func(t *testing.T) {
		s := newTestShimWithHypervisors(t, http.StatusCreated, `{}`)
		body := `{"name":""}`
		req := httptest.NewRequest(http.MethodPost, "/resource_providers", strings.NewReader(body))
		w := httptest.NewRecorder()
		s.HandleCreateResourceProvider(w, req)
		if w.Code != http.StatusBadRequest {
			t.Fatalf("status = %d, want %d", w.Code, http.StatusBadRequest)
		}
	})

	t.Run("invalid JSON returns 400", func(t *testing.T) {
		s := newTestShimWithHypervisors(t, http.StatusCreated, `{}`)
		req := httptest.NewRequest(http.MethodPost, "/resource_providers", strings.NewReader("not json"))
		w := httptest.NewRecorder()
		s.HandleCreateResourceProvider(w, req)
		if w.Code != http.StatusBadRequest {
			t.Fatalf("status = %d, want %d", w.Code, http.StatusBadRequest)
		}
	})
}

func TestHandleShowResourceProvider(t *testing.T) {
	hv1Obj := &hv1.Hypervisor{
		ObjectMeta: metav1.ObjectMeta{Name: "hv-show"},
		Status:     hv1.HypervisorStatus{HypervisorID: validUUID},
	}

	t.Run("found in k8s", func(t *testing.T) {
		s := newTestShimWithHypervisors(t, http.StatusOK, `{}`, hv1Obj)
		w := serveHandler(t, http.MethodGet, "/resource_providers/{uuid}",
			s.HandleShowResourceProvider, "/resource_providers/"+validUUID)
		if w.Code != http.StatusOK {
			t.Fatalf("status = %d, want %d", w.Code, http.StatusOK)
		}
		var rp resourceProvider
		if err := json.Unmarshal(w.Body.Bytes(), &rp); err != nil {
			t.Fatalf("failed to decode: %v", err)
		}
		if rp.Name != "hv-show" {
			t.Errorf("name = %q, want %q", rp.Name, "hv-show")
		}
		if rp.UUID != validUUID {
			t.Errorf("uuid = %q, want %q", rp.UUID, validUUID)
		}
	})

	t.Run("not in k8s forwards to upstream", func(t *testing.T) {
		const otherUUID = "a1b2c3d4-e5f6-7890-abcd-ef1234567890"
		upstreamBody := `{"uuid":"` + otherUUID + `","name":"upstream-rp","generation":0,"links":[]}`
		s := newTestShimWithHypervisors(t, http.StatusOK, upstreamBody, hv1Obj)
		w := serveHandler(t, http.MethodGet, "/resource_providers/{uuid}",
			s.HandleShowResourceProvider, "/resource_providers/"+otherUUID)
		if w.Code != http.StatusOK {
			t.Fatalf("status = %d, want %d", w.Code, http.StatusOK)
		}
		if !strings.Contains(w.Body.String(), "upstream-rp") {
			t.Errorf("expected upstream response body, got %q", w.Body.String())
		}
	})

	t.Run("invalid UUID returns 400", func(t *testing.T) {
		s := newTestShimWithHypervisors(t, http.StatusOK, `{}`)
		w := serveHandler(t, http.MethodGet, "/resource_providers/{uuid}",
			s.HandleShowResourceProvider, "/resource_providers/not-a-uuid")
		if w.Code != http.StatusBadRequest {
			t.Fatalf("status = %d, want %d", w.Code, http.StatusBadRequest)
		}
	})
}

func TestHandleUpdateResourceProvider(t *testing.T) {
	hv1Obj := &hv1.Hypervisor{
		ObjectMeta: metav1.ObjectMeta{Name: "hv-update"},
		Status:     hv1.HypervisorStatus{HypervisorID: validUUID},
	}

	t.Run("no-op update returns current state", func(t *testing.T) {
		s := newTestShimWithHypervisors(t, http.StatusOK, `{}`, hv1Obj)
		body := `{"name":"hv-update"}`
		req := httptest.NewRequest(http.MethodPut, "/resource_providers/"+validUUID, strings.NewReader(body))
		mux := http.NewServeMux()
		mux.HandleFunc("PUT /resource_providers/{uuid}", s.HandleUpdateResourceProvider)
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, req)
		if w.Code != http.StatusOK {
			t.Fatalf("status = %d, want %d", w.Code, http.StatusOK)
		}
		var rp resourceProvider
		if err := json.Unmarshal(w.Body.Bytes(), &rp); err != nil {
			t.Fatalf("failed to decode: %v", err)
		}
		if rp.Name != "hv-update" {
			t.Errorf("name = %q, want %q", rp.Name, "hv-update")
		}
	})

	t.Run("name change returns 409", func(t *testing.T) {
		s := newTestShimWithHypervisors(t, http.StatusOK, `{}`, hv1Obj)
		body := `{"name":"different-name"}`
		req := httptest.NewRequest(http.MethodPut, "/resource_providers/"+validUUID, strings.NewReader(body))
		mux := http.NewServeMux()
		mux.HandleFunc("PUT /resource_providers/{uuid}", s.HandleUpdateResourceProvider)
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, req)
		if w.Code != http.StatusConflict {
			t.Fatalf("status = %d, want %d", w.Code, http.StatusConflict)
		}
	})

	t.Run("parent change returns 409", func(t *testing.T) {
		s := newTestShimWithHypervisors(t, http.StatusOK, `{}`, hv1Obj)
		body := `{"name":"hv-update","parent_provider_uuid":"other-parent-uuid"}`
		req := httptest.NewRequest(http.MethodPut, "/resource_providers/"+validUUID, strings.NewReader(body))
		mux := http.NewServeMux()
		mux.HandleFunc("PUT /resource_providers/{uuid}", s.HandleUpdateResourceProvider)
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, req)
		if w.Code != http.StatusConflict {
			t.Fatalf("status = %d, want %d", w.Code, http.StatusConflict)
		}
	})

	t.Run("unknown UUID forwards to upstream", func(t *testing.T) {
		const otherUUID = "a1b2c3d4-e5f6-7890-abcd-ef1234567890"
		s := newTestShimWithHypervisors(t, http.StatusOK, `{"uuid":"`+otherUUID+`","name":"upstream"}`, hv1Obj)
		body := `{"name":"upstream"}`
		req := httptest.NewRequest(http.MethodPut, "/resource_providers/"+otherUUID, strings.NewReader(body))
		mux := http.NewServeMux()
		mux.HandleFunc("PUT /resource_providers/{uuid}", s.HandleUpdateResourceProvider)
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, req)
		if w.Code != http.StatusOK {
			t.Fatalf("status = %d, want %d", w.Code, http.StatusOK)
		}
	})

	t.Run("missing name returns 400", func(t *testing.T) {
		s := newTestShimWithHypervisors(t, http.StatusOK, `{}`)
		body := `{"name":""}`
		req := httptest.NewRequest(http.MethodPut, "/resource_providers/"+validUUID, strings.NewReader(body))
		mux := http.NewServeMux()
		mux.HandleFunc("PUT /resource_providers/{uuid}", s.HandleUpdateResourceProvider)
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, req)
		if w.Code != http.StatusBadRequest {
			t.Fatalf("status = %d, want %d", w.Code, http.StatusBadRequest)
		}
	})

	t.Run("invalid JSON returns 400", func(t *testing.T) {
		s := newTestShimWithHypervisors(t, http.StatusOK, `{}`)
		req := httptest.NewRequest(http.MethodPut, "/resource_providers/"+validUUID, strings.NewReader("not json"))
		mux := http.NewServeMux()
		mux.HandleFunc("PUT /resource_providers/{uuid}", s.HandleUpdateResourceProvider)
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, req)
		if w.Code != http.StatusBadRequest {
			t.Fatalf("status = %d, want %d", w.Code, http.StatusBadRequest)
		}
	})
}

func TestHandleDeleteResourceProvider(t *testing.T) {
	hv1Obj := &hv1.Hypervisor{
		ObjectMeta: metav1.ObjectMeta{Name: "hv-delete"},
		Status:     hv1.HypervisorStatus{HypervisorID: validUUID},
	}

	t.Run("k8s hypervisor returns 409", func(t *testing.T) {
		s := newTestShimWithHypervisors(t, http.StatusNoContent, "", hv1Obj)
		w := serveHandler(t, http.MethodDelete, "/resource_providers/{uuid}",
			s.HandleDeleteResourceProvider, "/resource_providers/"+validUUID)
		if w.Code != http.StatusConflict {
			t.Fatalf("status = %d, want %d", w.Code, http.StatusConflict)
		}
	})

	t.Run("unknown UUID forwards to upstream", func(t *testing.T) {
		const otherUUID = "a1b2c3d4-e5f6-7890-abcd-ef1234567890"
		s := newTestShimWithHypervisors(t, http.StatusNoContent, "", hv1Obj)
		w := serveHandler(t, http.MethodDelete, "/resource_providers/{uuid}",
			s.HandleDeleteResourceProvider, "/resource_providers/"+otherUUID)
		if w.Code != http.StatusNoContent {
			t.Fatalf("status = %d, want %d", w.Code, http.StatusNoContent)
		}
	})

	t.Run("invalid UUID returns 400", func(t *testing.T) {
		s := newTestShimWithHypervisors(t, http.StatusNoContent, "")
		w := serveHandler(t, http.MethodDelete, "/resource_providers/{uuid}",
			s.HandleDeleteResourceProvider, "/resource_providers/not-a-uuid")
		if w.Code != http.StatusBadRequest {
			t.Fatalf("status = %d, want %d", w.Code, http.StatusBadRequest)
		}
	})
}

// ---------------------------------------------------------------------------
// Feature flag tests
// ---------------------------------------------------------------------------

func TestHandleResourceProviders_FeatureFlagOff(t *testing.T) {
	hv1Obj := &hv1.Hypervisor{
		ObjectMeta: metav1.ObjectMeta{Name: "hv-flagtest"},
		Status:     hv1.HypervisorStatus{HypervisorID: validUUID},
	}

	newFlagOffShim := func(t *testing.T, upstreamStatus int, upstreamBody string) *Shim {
		t.Helper()
		upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(upstreamStatus)
			if _, err := w.Write([]byte(upstreamBody)); err != nil {
				t.Errorf("failed to write upstream body: %v", err)
			}
		}))
		t.Cleanup(upstream.Close)
		down, up := newTestTimers()
		return &Shim{
			Client: newFakeClient(t, hv1Obj),
			config: config{
				PlacementURL: upstream.URL,
				Features:     featuresConfig{EnableResourceProviders: false},
			},
			httpClient:             upstream.Client(),
			maxBodyLogSize:         4096,
			downstreamRequestTimer: down,
			upstreamRequestTimer:   up,
		}
	}

	t.Run("create forwards to upstream", func(t *testing.T) {
		s := newFlagOffShim(t, http.StatusCreated, `{"uuid":"new","name":"hv-flagtest"}`)
		body := `{"name":"hv-flagtest"}`
		req := httptest.NewRequest(http.MethodPost, "/resource_providers", strings.NewReader(body))
		w := httptest.NewRecorder()
		s.HandleCreateResourceProvider(w, req)
		if w.Code != http.StatusCreated {
			t.Fatalf("status = %d, want %d (flag off should forward, not 409)", w.Code, http.StatusCreated)
		}
	})

	t.Run("show forwards to upstream", func(t *testing.T) {
		s := newFlagOffShim(t, http.StatusOK, `{"uuid":"`+validUUID+`","name":"upstream-rp"}`)
		w := serveHandler(t, http.MethodGet, "/resource_providers/{uuid}",
			s.HandleShowResourceProvider, "/resource_providers/"+validUUID)
		if w.Code != http.StatusOK {
			t.Fatalf("status = %d, want %d", w.Code, http.StatusOK)
		}
		if !strings.Contains(w.Body.String(), "upstream-rp") {
			t.Errorf("expected upstream body, got %q", w.Body.String())
		}
	})

	t.Run("update forwards to upstream", func(t *testing.T) {
		s := newFlagOffShim(t, http.StatusOK, `{"uuid":"`+validUUID+`","name":"different-name"}`)
		body := `{"name":"different-name"}`
		req := httptest.NewRequest(http.MethodPut, "/resource_providers/"+validUUID, strings.NewReader(body))
		mux := http.NewServeMux()
		mux.HandleFunc("PUT /resource_providers/{uuid}", s.HandleUpdateResourceProvider)
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, req)
		if w.Code != http.StatusOK {
			t.Fatalf("status = %d, want %d (flag off should forward, not 409)", w.Code, http.StatusOK)
		}
	})

	t.Run("delete forwards to upstream", func(t *testing.T) {
		s := newFlagOffShim(t, http.StatusNoContent, "")
		w := serveHandler(t, http.MethodDelete, "/resource_providers/{uuid}",
			s.HandleDeleteResourceProvider, "/resource_providers/"+validUUID)
		if w.Code != http.StatusNoContent {
			t.Fatalf("status = %d, want %d (flag off should forward, not 409)", w.Code, http.StatusNoContent)
		}
	})

	t.Run("list forwards to upstream without merge", func(t *testing.T) {
		upstreamBody := `{"resource_providers":[{"uuid":"upstream-uuid","name":"upstream-rp","generation":1,"links":[]}]}`
		s := newFlagOffShim(t, http.StatusOK, upstreamBody)
		w := serveHandler(t, http.MethodGet, "/resource_providers",
			s.HandleListResourceProviders, "/resource_providers")
		if w.Code != http.StatusOK {
			t.Fatalf("status = %d, want %d", w.Code, http.StatusOK)
		}
		if !strings.Contains(w.Body.String(), "upstream-uuid") {
			t.Errorf("expected upstream body passthrough, got %q", w.Body.String())
		}
		if strings.Contains(w.Body.String(), validUUID) {
			t.Errorf("should not contain k8s hypervisor UUID when flag is off, got %q", w.Body.String())
		}
	})
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

func names(hvs []hv1.Hypervisor) []string {
	out := make([]string, len(hvs))
	for i, hv := range hvs {
		out[i] = hv.Name
	}
	return out
}
