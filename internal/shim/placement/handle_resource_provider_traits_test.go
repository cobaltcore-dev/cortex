// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package placement

import (
	"encoding/json"
	"net/http"
	"testing"
)

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
	s := newTestShim(t, http.StatusOK, `{"traits":["CUSTOM_HW_FPGA"],"resource_provider_generation":1}`, nil)
	s.config.Features.ResourceProviderTraits = FeatureModeHybrid
	t.Run("GET forwards to upstream", func(t *testing.T) {
		w := serveHandler(t, "GET", "/resource_providers/{uuid}/traits",
			s.HandleListResourceProviderTraits,
			"/resource_providers/"+validUUID+"/traits")
		if w.Code != http.StatusOK {
			t.Fatalf("status = %d, want %d", w.Code, http.StatusOK)
		}
	})
	t.Run("PUT forwards to upstream", func(t *testing.T) {
		w := serveHandler(t, "PUT", "/resource_providers/{uuid}/traits",
			s.HandleUpdateResourceProviderTraits,
			"/resource_providers/"+validUUID+"/traits")
		if w.Code != http.StatusOK {
			t.Fatalf("status = %d, want %d", w.Code, http.StatusOK)
		}
	})

	sDel := newTestShim(t, http.StatusNoContent, "", nil)
	sDel.config.Features.ResourceProviderTraits = FeatureModeHybrid
	t.Run("DELETE forwards to upstream", func(t *testing.T) {
		w := serveHandler(t, "DELETE", "/resource_providers/{uuid}/traits",
			sDel.HandleDeleteResourceProviderTraits,
			"/resource_providers/"+validUUID+"/traits")
		if w.Code != http.StatusNoContent {
			t.Fatalf("status = %d, want %d", w.Code, http.StatusNoContent)
		}
	})
}

func TestHandleResourceProviderTraits_CRDMode(t *testing.T) {
	hv := testHypervisorFull("kvm-host-1", validUUID, nil, []string{"CUSTOM_HW_FPGA", "HW_CPU_X86_SSE42"}, nil)
	s := newTestShimWithHypervisors(t, http.StatusOK, "{}", &hv)
	s.config.Features.ResourceProviderTraits = FeatureModeCRD
	s.config.Features.ResourceProviders = FeatureModeCRD

	t.Run("GET returns traits from CRD for KVM provider", func(t *testing.T) {
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
	})
	t.Run("GET returns 404 for non-KVM provider", func(t *testing.T) {
		nonKVMUUID := "a1b2c3d4-e5f6-7890-abcd-ef1234567890"
		w := serveHandler(t, "GET", "/resource_providers/{uuid}/traits",
			s.HandleListResourceProviderTraits,
			"/resource_providers/"+nonKVMUUID+"/traits")
		if w.Code != http.StatusNotFound {
			t.Fatalf("status = %d, want %d", w.Code, http.StatusNotFound)
		}
	})
	t.Run("PUT returns 501", func(t *testing.T) {
		w := serveHandler(t, "PUT", "/resource_providers/{uuid}/traits",
			s.HandleUpdateResourceProviderTraits,
			"/resource_providers/"+validUUID+"/traits")
		if w.Code != http.StatusNotImplemented {
			t.Fatalf("status = %d, want %d", w.Code, http.StatusNotImplemented)
		}
	})
	t.Run("DELETE returns 501", func(t *testing.T) {
		w := serveHandler(t, "DELETE", "/resource_providers/{uuid}/traits",
			s.HandleDeleteResourceProviderTraits,
			"/resource_providers/"+validUUID+"/traits")
		if w.Code != http.StatusNotImplemented {
			t.Fatalf("status = %d, want %d", w.Code, http.StatusNotImplemented)
		}
	})
}
