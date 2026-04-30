// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package placement

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	hv1 "github.com/cobaltcore-dev/openstack-hypervisor-operator/api/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

func testHypervisorWithGroups(name, openstackID string, groups []hv1.Group) *hv1.Hypervisor {
	return &hv1.Hypervisor{
		ObjectMeta: metav1.ObjectMeta{Name: name},
		Spec:       hv1.HypervisorSpec{Groups: groups},
		Status:     hv1.HypervisorStatus{HypervisorID: openstackID},
	}
}

func serveHandlerWithBody(t *testing.T, method, pattern string, handler http.HandlerFunc, reqPath, body string) *httptest.ResponseRecorder { //nolint:unparam
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc(method+" "+pattern, handler)
	var req *http.Request
	if body != "" {
		req = httptest.NewRequest(method, reqPath, strings.NewReader(body))
	} else {
		req = httptest.NewRequest(method, reqPath, http.NoBody)
	}
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	return w
}

func TestHandleListResourceProviderTraits(t *testing.T) {
	t.Run("valid uuid", func(t *testing.T) {
		s := newTestShim(t, http.StatusOK, "{}", nil)
		w := serveHandler(t, "GET", "/resource_providers/{uuid}/traits",
			s.HandleListResourceProviderTraits,
			"/resource_providers/"+validUUID+"/traits")
		if w.Code != http.StatusOK {
			t.Fatalf("status = %d, want %d", w.Code, http.StatusOK)
		}
	})
	t.Run("invalid uuid", func(t *testing.T) {
		s := newTestShim(t, http.StatusOK, "{}", nil)
		w := serveHandler(t, "GET", "/resource_providers/{uuid}/traits",
			s.HandleListResourceProviderTraits,
			"/resource_providers/not-a-uuid/traits")
		if w.Code != http.StatusBadRequest {
			t.Fatalf("status = %d, want %d", w.Code, http.StatusBadRequest)
		}
	})
}

func TestHandleUpdateResourceProviderTraits(t *testing.T) {
	t.Run("valid uuid", func(t *testing.T) {
		s := newTestShim(t, http.StatusOK, "{}", nil)
		w := serveHandler(t, "PUT", "/resource_providers/{uuid}/traits",
			s.HandleUpdateResourceProviderTraits,
			"/resource_providers/"+validUUID+"/traits")
		if w.Code != http.StatusOK {
			t.Fatalf("status = %d, want %d", w.Code, http.StatusOK)
		}
	})
	t.Run("invalid uuid", func(t *testing.T) {
		s := newTestShim(t, http.StatusOK, "{}", nil)
		w := serveHandler(t, "PUT", "/resource_providers/{uuid}/traits",
			s.HandleUpdateResourceProviderTraits,
			"/resource_providers/not-a-uuid/traits")
		if w.Code != http.StatusBadRequest {
			t.Fatalf("status = %d, want %d", w.Code, http.StatusBadRequest)
		}
	})
}

func TestHandleDeleteResourceProviderTraits(t *testing.T) {
	t.Run("valid uuid", func(t *testing.T) {
		s := newTestShim(t, http.StatusNoContent, "", nil)
		w := serveHandler(t, "DELETE", "/resource_providers/{uuid}/traits",
			s.HandleDeleteResourceProviderTraits,
			"/resource_providers/"+validUUID+"/traits")
		if w.Code != http.StatusNoContent {
			t.Fatalf("status = %d, want %d", w.Code, http.StatusNoContent)
		}
	})
	t.Run("invalid uuid", func(t *testing.T) {
		s := newTestShim(t, http.StatusOK, "{}", nil)
		w := serveHandler(t, "DELETE", "/resource_providers/{uuid}/traits",
			s.HandleDeleteResourceProviderTraits,
			"/resource_providers/not-a-uuid/traits")
		if w.Code != http.StatusBadRequest {
			t.Fatalf("status = %d, want %d", w.Code, http.StatusBadRequest)
		}
	})
}

func TestHandleResourceProviderTraits_HybridMode(t *testing.T) {
	s := newTestShimWithHypervisors(t, http.StatusOK, `{"traits":["CUSTOM_HW_FPGA"],"resource_provider_generation":1}`)
	s.config.Features.ResourceProviderTraits = FeatureModeHybrid
	t.Run("GET forwards to upstream when provider not in CRD", func(t *testing.T) {
		w := serveHandler(t, "GET", "/resource_providers/{uuid}/traits",
			s.HandleListResourceProviderTraits,
			"/resource_providers/"+validUUID+"/traits")
		if w.Code != http.StatusOK {
			t.Fatalf("status = %d, want %d", w.Code, http.StatusOK)
		}
	})
	t.Run("PUT forwards to upstream when provider not in CRD", func(t *testing.T) {
		w := serveHandler(t, "PUT", "/resource_providers/{uuid}/traits",
			s.HandleUpdateResourceProviderTraits,
			"/resource_providers/"+validUUID+"/traits")
		if w.Code != http.StatusOK {
			t.Fatalf("status = %d, want %d", w.Code, http.StatusOK)
		}
	})

	sDel := newTestShimWithHypervisors(t, http.StatusNoContent, "")
	sDel.config.Features.ResourceProviderTraits = FeatureModeHybrid
	t.Run("DELETE forwards to upstream when provider not in CRD", func(t *testing.T) {
		w := serveHandler(t, "DELETE", "/resource_providers/{uuid}/traits",
			sDel.HandleDeleteResourceProviderTraits,
			"/resource_providers/"+validUUID+"/traits")
		if w.Code != http.StatusNoContent {
			t.Fatalf("status = %d, want %d", w.Code, http.StatusNoContent)
		}
	})

	t.Run("GET serves from CRD when provider is KVM", func(t *testing.T) {
		hv := testHypervisorWithGroups("kvm-hybrid", validUUID, []hv1.Group{
			{Trait: &hv1.TraitGroup{Name: "CUSTOM_KVM_TRAIT"}},
		})
		sKVM := newTestShimWithHypervisors(t, http.StatusOK, "{}", hv)
		sKVM.config.Features.ResourceProviderTraits = FeatureModeHybrid
		w := serveHandler(t, "GET", "/resource_providers/{uuid}/traits",
			sKVM.HandleListResourceProviderTraits,
			"/resource_providers/"+validUUID+"/traits")
		if w.Code != http.StatusOK {
			t.Fatalf("status = %d, want %d", w.Code, http.StatusOK)
		}
		var resp resourceProviderTraitsResponse
		if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
			t.Fatalf("failed to decode response: %v", err)
		}
		if len(resp.Traits) != 1 || resp.Traits[0] != "CUSTOM_KVM_TRAIT" {
			t.Fatalf("expected [CUSTOM_KVM_TRAIT], got %v", resp.Traits)
		}
	})
}

func TestHandleResourceProviderTraits_CRDMode(t *testing.T) {
	groups := []hv1.Group{
		{Trait: &hv1.TraitGroup{Name: "CUSTOM_HW_FPGA"}},
		{Trait: &hv1.TraitGroup{Name: "HW_CPU_X86_SSE42"}},
		{Aggregate: &hv1.AggregateGroup{Name: "az1", UUID: "agg-uuid-1"}},
	}
	hv := testHypervisorWithGroups("kvm-host-1", validUUID, groups)
	s := newTestShimWithHypervisors(t, http.StatusOK, "{}", hv)
	s.config.Features.ResourceProviderTraits = FeatureModeCRD
	s.config.Features.ResourceProviders = FeatureModeCRD

	t.Run("GET returns traits from spec.groups", func(t *testing.T) {
		w := serveHandler(t, "GET", "/resource_providers/{uuid}/traits",
			s.HandleListResourceProviderTraits,
			"/resource_providers/"+validUUID+"/traits")
		if w.Code != http.StatusOK {
			t.Fatalf("status = %d, want %d", w.Code, http.StatusOK)
		}
		var resp resourceProviderTraitsResponse
		if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
			t.Fatalf("failed to decode response: %v", err)
		}
		if len(resp.Traits) != 2 {
			t.Fatalf("traits count = %d, want 2", len(resp.Traits))
		}
		if resp.Traits[0] != "CUSTOM_HW_FPGA" {
			t.Errorf("traits[0] = %q, want CUSTOM_HW_FPGA", resp.Traits[0])
		}
		if resp.Traits[1] != "HW_CPU_X86_SSE42" {
			t.Errorf("traits[1] = %q, want HW_CPU_X86_SSE42", resp.Traits[1])
		}
	})

	t.Run("GET returns empty traits when spec.groups has no traits", func(t *testing.T) {
		hvNoTraits := testHypervisorWithGroups("kvm-no-traits", "b1b2b3b4-c5c6-d7d8-e9e0-f1f2f3f4f5f6", []hv1.Group{
			{Aggregate: &hv1.AggregateGroup{Name: "az1", UUID: "agg-1"}},
		})
		s2 := newTestShimWithHypervisors(t, http.StatusOK, "{}", hvNoTraits)
		s2.config.Features.ResourceProviderTraits = FeatureModeCRD
		w := serveHandler(t, "GET", "/resource_providers/{uuid}/traits",
			s2.HandleListResourceProviderTraits,
			"/resource_providers/b1b2b3b4-c5c6-d7d8-e9e0-f1f2f3f4f5f6/traits")
		if w.Code != http.StatusOK {
			t.Fatalf("status = %d, want %d", w.Code, http.StatusOK)
		}
		var resp resourceProviderTraitsResponse
		if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
			t.Fatalf("failed to decode response: %v", err)
		}
		if len(resp.Traits) != 0 {
			t.Fatalf("traits count = %d, want 0", len(resp.Traits))
		}
	})

	t.Run("GET returns 404 for non-existent provider", func(t *testing.T) {
		nonKVMUUID := "a1b2c3d4-e5f6-7890-abcd-ef1234567890"
		w := serveHandler(t, "GET", "/resource_providers/{uuid}/traits",
			s.HandleListResourceProviderTraits,
			"/resource_providers/"+nonKVMUUID+"/traits")
		if w.Code != http.StatusNotFound {
			t.Fatalf("status = %d, want %d", w.Code, http.StatusNotFound)
		}
	})

	t.Run("PUT replaces traits in spec.groups preserving aggregates", func(t *testing.T) {
		hvPut := testHypervisorWithGroups("kvm-put-traits", "c1c2c3c4-d5d6-e7e8-f9f0-a1a2a3a4a5a6", []hv1.Group{
			{Trait: &hv1.TraitGroup{Name: "OLD_TRAIT"}},
			{Aggregate: &hv1.AggregateGroup{Name: "keep-me", UUID: "keep-uuid"}},
		})
		sPut := newTestShimWithHypervisors(t, http.StatusOK, "{}", hvPut)
		sPut.config.Features.ResourceProviderTraits = FeatureModeCRD

		body := `{"traits":["NEW_TRAIT_1","NEW_TRAIT_2"],"resource_provider_generation":0}`
		w := serveHandlerWithBody(t, "PUT", "/resource_providers/{uuid}/traits",
			sPut.HandleUpdateResourceProviderTraits,
			"/resource_providers/c1c2c3c4-d5d6-e7e8-f9f0-a1a2a3a4a5a6/traits", body)
		if w.Code != http.StatusOK {
			t.Fatalf("status = %d, want %d; body: %s", w.Code, http.StatusOK, w.Body.String())
		}
		var resp resourceProviderTraitsResponse
		if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
			t.Fatalf("failed to decode response: %v", err)
		}
		if len(resp.Traits) != 2 {
			t.Fatalf("traits count = %d, want 2", len(resp.Traits))
		}

		// Verify aggregates were preserved by fetching the updated object.
		var updated hv1.Hypervisor
		if err := sPut.Get(t.Context(), client.ObjectKeyFromObject(hvPut), &updated); err != nil {
			t.Fatalf("failed to get updated hypervisor: %v", err)
		}
		aggs := hv1.GetAggregates(updated.Spec.Groups)
		if len(aggs) != 1 || aggs[0].UUID != "keep-uuid" {
			t.Fatalf("aggregates were not preserved: got %+v", aggs)
		}
	})

	t.Run("PUT returns 409 on generation mismatch", func(t *testing.T) {
		hvConflict := testHypervisorWithGroups("kvm-conflict", "d1d2d3d4-e5e6-f7f8-a9a0-b1b2b3b4b5b6", nil)
		sConflict := newTestShimWithHypervisors(t, http.StatusOK, "{}", hvConflict)
		sConflict.config.Features.ResourceProviderTraits = FeatureModeCRD

		body := `{"traits":["T1"],"resource_provider_generation":999}`
		w := serveHandlerWithBody(t, "PUT", "/resource_providers/{uuid}/traits",
			sConflict.HandleUpdateResourceProviderTraits,
			"/resource_providers/d1d2d3d4-e5e6-f7f8-a9a0-b1b2b3b4b5b6/traits", body)
		if w.Code != http.StatusConflict {
			t.Fatalf("status = %d, want %d", w.Code, http.StatusConflict)
		}
	})

	t.Run("PUT returns 404 for non-existent provider", func(t *testing.T) {
		body := `{"traits":["T1"],"resource_provider_generation":0}`
		w := serveHandlerWithBody(t, "PUT", "/resource_providers/{uuid}/traits",
			s.HandleUpdateResourceProviderTraits,
			"/resource_providers/e1e2e3e4-f5f6-a7a8-b9b0-c1c2c3c4c5c6/traits", body)
		if w.Code != http.StatusNotFound {
			t.Fatalf("status = %d, want %d", w.Code, http.StatusNotFound)
		}
	})

	t.Run("PUT returns 400 for malformed body", func(t *testing.T) {
		w := serveHandlerWithBody(t, "PUT", "/resource_providers/{uuid}/traits",
			s.HandleUpdateResourceProviderTraits,
			"/resource_providers/"+validUUID+"/traits", "not json")
		if w.Code != http.StatusBadRequest {
			t.Fatalf("status = %d, want %d", w.Code, http.StatusBadRequest)
		}
	})

	t.Run("DELETE removes all traits preserving aggregates", func(t *testing.T) {
		hvDel := testHypervisorWithGroups("kvm-del-traits", "f1f2f3f4-a5a6-b7b8-c9c0-d1d2d3d4d5d6", []hv1.Group{
			{Trait: &hv1.TraitGroup{Name: "REMOVE_ME"}},
			{Aggregate: &hv1.AggregateGroup{Name: "stay", UUID: "stay-uuid"}},
		})
		sDel := newTestShimWithHypervisors(t, http.StatusOK, "{}", hvDel)
		sDel.config.Features.ResourceProviderTraits = FeatureModeCRD

		w := serveHandler(t, "DELETE", "/resource_providers/{uuid}/traits",
			sDel.HandleDeleteResourceProviderTraits,
			"/resource_providers/f1f2f3f4-a5a6-b7b8-c9c0-d1d2d3d4d5d6/traits")
		if w.Code != http.StatusNoContent {
			t.Fatalf("status = %d, want %d", w.Code, http.StatusNoContent)
		}

		var updated hv1.Hypervisor
		if err := sDel.Get(t.Context(), client.ObjectKeyFromObject(hvDel), &updated); err != nil {
			t.Fatalf("failed to get updated hypervisor: %v", err)
		}
		traits := hv1.GetTraits(updated.Spec.Groups)
		if len(traits) != 0 {
			t.Fatalf("expected no traits, got %+v", traits)
		}
		aggs := hv1.GetAggregates(updated.Spec.Groups)
		if len(aggs) != 1 || aggs[0].UUID != "stay-uuid" {
			t.Fatalf("aggregates were not preserved: got %+v", aggs)
		}
	})
}
