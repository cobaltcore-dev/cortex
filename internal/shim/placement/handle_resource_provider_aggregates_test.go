// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package placement

import (
	"encoding/json"
	"net/http"
	"testing"

	hv1 "github.com/cobaltcore-dev/openstack-hypervisor-operator/api/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

func TestHandleListResourceProviderAggregates(t *testing.T) {
	t.Run("valid uuid", func(t *testing.T) {
		s := newTestShim(t, http.StatusOK, "{}", nil)
		w := serveHandler(t, "GET", "/resource_providers/{uuid}/aggregates",
			s.HandleListResourceProviderAggregates,
			"/resource_providers/"+validUUID+"/aggregates")
		if w.Code != http.StatusOK {
			t.Fatalf("status = %d, want %d", w.Code, http.StatusOK)
		}
	})
	t.Run("invalid uuid", func(t *testing.T) {
		s := newTestShim(t, http.StatusOK, "{}", nil)
		w := serveHandler(t, "GET", "/resource_providers/{uuid}/aggregates",
			s.HandleListResourceProviderAggregates,
			"/resource_providers/not-a-uuid/aggregates")
		if w.Code != http.StatusBadRequest {
			t.Fatalf("status = %d, want %d", w.Code, http.StatusBadRequest)
		}
	})
}

func TestHandleUpdateResourceProviderAggregates(t *testing.T) {
	t.Run("valid uuid", func(t *testing.T) {
		s := newTestShim(t, http.StatusOK, "{}", nil)
		w := serveHandler(t, "PUT", "/resource_providers/{uuid}/aggregates",
			s.HandleUpdateResourceProviderAggregates,
			"/resource_providers/"+validUUID+"/aggregates")
		if w.Code != http.StatusOK {
			t.Fatalf("status = %d, want %d", w.Code, http.StatusOK)
		}
	})
	t.Run("invalid uuid", func(t *testing.T) {
		s := newTestShim(t, http.StatusOK, "{}", nil)
		w := serveHandler(t, "PUT", "/resource_providers/{uuid}/aggregates",
			s.HandleUpdateResourceProviderAggregates,
			"/resource_providers/not-a-uuid/aggregates")
		if w.Code != http.StatusBadRequest {
			t.Fatalf("status = %d, want %d", w.Code, http.StatusBadRequest)
		}
	})
}

func TestHandleResourceProviderAggregates_HybridMode(t *testing.T) {
	s := newTestShimWithHypervisors(t, http.StatusOK, `{"aggregates":["uuid-1"],"resource_provider_generation":1}`)
	s.config.Features.Aggregates = FeatureModeHybrid
	t.Run("GET forwards to upstream when provider not in CRD", func(t *testing.T) {
		w := serveHandler(t, "GET", "/resource_providers/{uuid}/aggregates",
			s.HandleListResourceProviderAggregates,
			"/resource_providers/"+validUUID+"/aggregates")
		if w.Code != http.StatusOK {
			t.Fatalf("status = %d, want %d", w.Code, http.StatusOK)
		}
	})
	t.Run("PUT forwards to upstream when provider not in CRD", func(t *testing.T) {
		w := serveHandler(t, "PUT", "/resource_providers/{uuid}/aggregates",
			s.HandleUpdateResourceProviderAggregates,
			"/resource_providers/"+validUUID+"/aggregates")
		if w.Code != http.StatusOK {
			t.Fatalf("status = %d, want %d", w.Code, http.StatusOK)
		}
	})

	t.Run("GET serves from CRD when provider is KVM", func(t *testing.T) {
		hv := testHypervisorWithGroups("kvm-hybrid-agg", validUUID, []hv1.Group{
			{Aggregate: &hv1.AggregateGroup{Name: "az-west", UUID: "agg-uuid-1"}},
		})
		sKVM := newTestShimWithHypervisors(t, http.StatusOK, "{}", hv)
		sKVM.config.Features.Aggregates = FeatureModeHybrid
		w := serveHandler(t, "GET", "/resource_providers/{uuid}/aggregates",
			sKVM.HandleListResourceProviderAggregates,
			"/resource_providers/"+validUUID+"/aggregates")
		if w.Code != http.StatusOK {
			t.Fatalf("status = %d, want %d", w.Code, http.StatusOK)
		}
		var resp resourceProviderAggregatesResponse
		if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
			t.Fatalf("failed to decode response: %v", err)
		}
		if len(resp.Aggregates) != 1 || resp.Aggregates[0] != "agg-uuid-1" {
			t.Fatalf("expected [agg-uuid-1], got %v", resp.Aggregates)
		}
	})
}

func TestHandleResourceProviderAggregates_CRDMode(t *testing.T) {
	groups := []hv1.Group{
		{Trait: &hv1.TraitGroup{Name: "HW_CPU_X86_AVX2"}},
		{Aggregate: &hv1.AggregateGroup{Name: "fast-storage", UUID: "agg-uuid-1"}},
		{Aggregate: &hv1.AggregateGroup{Name: "az-west", UUID: "agg-uuid-2"}},
	}
	hv := testHypervisorWithGroups("kvm-host-1", validUUID, groups)
	s := newTestShimWithHypervisors(t, http.StatusOK, "{}", hv)
	s.config.Features.Aggregates = FeatureModeCRD

	t.Run("GET returns aggregate UUIDs from spec.groups", func(t *testing.T) {
		w := serveHandler(t, "GET", "/resource_providers/{uuid}/aggregates",
			s.HandleListResourceProviderAggregates,
			"/resource_providers/"+validUUID+"/aggregates")
		if w.Code != http.StatusOK {
			t.Fatalf("status = %d, want %d", w.Code, http.StatusOK)
		}
		var resp resourceProviderAggregatesResponse
		if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
			t.Fatalf("failed to decode response: %v", err)
		}
		if len(resp.Aggregates) != 2 {
			t.Fatalf("aggregates count = %d, want 2", len(resp.Aggregates))
		}
		if resp.Aggregates[0] != "agg-uuid-1" {
			t.Errorf("aggregates[0] = %q, want agg-uuid-1", resp.Aggregates[0])
		}
		if resp.Aggregates[1] != "agg-uuid-2" {
			t.Errorf("aggregates[1] = %q, want agg-uuid-2", resp.Aggregates[1])
		}
	})

	t.Run("GET returns empty aggregates when spec.groups has no aggregates", func(t *testing.T) {
		hvNoAggs := testHypervisorWithGroups("kvm-no-aggs", "b1b2b3b4-c5c6-d7d8-e9e0-f1f2f3f4f5f6", []hv1.Group{
			{Trait: &hv1.TraitGroup{Name: "CUSTOM_T"}},
		})
		s2 := newTestShimWithHypervisors(t, http.StatusOK, "{}", hvNoAggs)
		s2.config.Features.Aggregates = FeatureModeCRD
		w := serveHandler(t, "GET", "/resource_providers/{uuid}/aggregates",
			s2.HandleListResourceProviderAggregates,
			"/resource_providers/b1b2b3b4-c5c6-d7d8-e9e0-f1f2f3f4f5f6/aggregates")
		if w.Code != http.StatusOK {
			t.Fatalf("status = %d, want %d", w.Code, http.StatusOK)
		}
		var resp resourceProviderAggregatesResponse
		if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
			t.Fatalf("failed to decode response: %v", err)
		}
		if len(resp.Aggregates) != 0 {
			t.Fatalf("aggregates count = %d, want 0", len(resp.Aggregates))
		}
	})

	t.Run("GET returns 404 for non-existent provider", func(t *testing.T) {
		nonExistUUID := "a1b2c3d4-e5f6-7890-abcd-ef1234567890"
		w := serveHandler(t, "GET", "/resource_providers/{uuid}/aggregates",
			s.HandleListResourceProviderAggregates,
			"/resource_providers/"+nonExistUUID+"/aggregates")
		if w.Code != http.StatusNotFound {
			t.Fatalf("status = %d, want %d", w.Code, http.StatusNotFound)
		}
	})

	t.Run("PUT replaces aggregates in spec.groups preserving traits", func(t *testing.T) {
		hvPut := testHypervisorWithGroups("kvm-put-aggs", "c1c2c3c4-d5d6-e7e8-f9f0-a1a2a3a4a5a6", []hv1.Group{
			{Aggregate: &hv1.AggregateGroup{Name: "old-agg", UUID: "old-uuid"}},
			{Trait: &hv1.TraitGroup{Name: "KEEP_TRAIT"}},
		})
		sPut := newTestShimWithHypervisors(t, http.StatusOK, "{}", hvPut)
		sPut.config.Features.Aggregates = FeatureModeCRD

		body := `{"aggregates":["new-uuid-1","new-uuid-2"],"resource_provider_generation":0}`
		w := serveHandlerWithBody(t, "PUT", "/resource_providers/{uuid}/aggregates",
			sPut.HandleUpdateResourceProviderAggregates,
			"/resource_providers/c1c2c3c4-d5d6-e7e8-f9f0-a1a2a3a4a5a6/aggregates", body)
		if w.Code != http.StatusOK {
			t.Fatalf("status = %d, want %d; body: %s", w.Code, http.StatusOK, w.Body.String())
		}
		var resp resourceProviderAggregatesResponse
		if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
			t.Fatalf("failed to decode response: %v", err)
		}
		if len(resp.Aggregates) != 2 {
			t.Fatalf("aggregates count = %d, want 2", len(resp.Aggregates))
		}

		// Verify traits were preserved.
		var updated hv1.Hypervisor
		if err := sPut.Get(t.Context(), client.ObjectKeyFromObject(hvPut), &updated); err != nil {
			t.Fatalf("failed to get updated hypervisor: %v", err)
		}
		traits := hv1.GetTraits(updated.Spec.Groups)
		if len(traits) != 1 || traits[0].Name != "KEEP_TRAIT" {
			t.Fatalf("traits were not preserved: got %+v", traits)
		}
	})

	t.Run("PUT returns 409 on generation mismatch", func(t *testing.T) {
		hvConflict := testHypervisorWithGroups("kvm-agg-conflict", "d1d2d3d4-e5e6-f7f8-a9a0-b1b2b3b4b5b6", nil)
		sConflict := newTestShimWithHypervisors(t, http.StatusOK, "{}", hvConflict)
		sConflict.config.Features.Aggregates = FeatureModeCRD

		body := `{"aggregates":["u1"],"resource_provider_generation":999}`
		w := serveHandlerWithBody(t, "PUT", "/resource_providers/{uuid}/aggregates",
			sConflict.HandleUpdateResourceProviderAggregates,
			"/resource_providers/d1d2d3d4-e5e6-f7f8-a9a0-b1b2b3b4b5b6/aggregates", body)
		if w.Code != http.StatusConflict {
			t.Fatalf("status = %d, want %d", w.Code, http.StatusConflict)
		}
	})

	t.Run("PUT returns 404 for non-existent provider", func(t *testing.T) {
		body := `{"aggregates":["u1"],"resource_provider_generation":0}`
		w := serveHandlerWithBody(t, "PUT", "/resource_providers/{uuid}/aggregates",
			s.HandleUpdateResourceProviderAggregates,
			"/resource_providers/e1e2e3e4-f5f6-a7a8-b9b0-c1c2c3c4c5c6/aggregates", body)
		if w.Code != http.StatusNotFound {
			t.Fatalf("status = %d, want %d", w.Code, http.StatusNotFound)
		}
	})

	t.Run("PUT with empty list removes all aggregates", func(t *testing.T) {
		hvClear := testHypervisorWithGroups("kvm-clear-aggs", "e1e2e3e4-f5f6-a7a8-b9b0-c1c2c3c4c5c6", []hv1.Group{
			{Aggregate: &hv1.AggregateGroup{Name: "remove-me", UUID: "remove-uuid"}},
			{Trait: &hv1.TraitGroup{Name: "KEEP_TRAIT"}},
		})
		sClear := newTestShimWithHypervisors(t, http.StatusOK, "{}", hvClear)
		sClear.config.Features.Aggregates = FeatureModeCRD

		body := `{"aggregates":[],"resource_provider_generation":0}`
		w := serveHandlerWithBody(t, "PUT", "/resource_providers/{uuid}/aggregates",
			sClear.HandleUpdateResourceProviderAggregates,
			"/resource_providers/e1e2e3e4-f5f6-a7a8-b9b0-c1c2c3c4c5c6/aggregates", body)
		if w.Code != http.StatusOK {
			t.Fatalf("status = %d, want %d; body: %s", w.Code, http.StatusOK, w.Body.String())
		}
		var resp resourceProviderAggregatesResponse
		if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
			t.Fatalf("failed to decode response: %v", err)
		}
		if len(resp.Aggregates) != 0 {
			t.Fatalf("expected 0 aggregates, got %d", len(resp.Aggregates))
		}

		var updated hv1.Hypervisor
		if err := sClear.Get(t.Context(), client.ObjectKeyFromObject(hvClear), &updated); err != nil {
			t.Fatalf("failed to get updated hypervisor: %v", err)
		}
		traits := hv1.GetTraits(updated.Spec.Groups)
		if len(traits) != 1 || traits[0].Name != "KEEP_TRAIT" {
			t.Fatalf("traits were not preserved: got %+v", traits)
		}
	})

	t.Run("PUT returns 400 for malformed body", func(t *testing.T) {
		w := serveHandlerWithBody(t, "PUT", "/resource_providers/{uuid}/aggregates",
			s.HandleUpdateResourceProviderAggregates,
			"/resource_providers/"+validUUID+"/aggregates", "not json")
		if w.Code != http.StatusBadRequest {
			t.Fatalf("status = %d, want %d", w.Code, http.StatusBadRequest)
		}
	})
}
