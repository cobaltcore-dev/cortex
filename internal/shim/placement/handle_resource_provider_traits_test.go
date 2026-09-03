// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package placement

import (
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
	t.Run("invalid uuid forwards when disabled", func(t *testing.T) {
		s := newTestShim(t, http.StatusOK, "{}", nil)
		w := serveHandler(t, "GET", "/resource_providers/{uuid}/traits",
			s.HandleListResourceProviderTraits,
			"/resource_providers/not-a-uuid/traits")
		if w.Code != http.StatusOK {
			t.Fatalf("status = %d, want %d", w.Code, http.StatusOK)
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
	t.Run("invalid uuid forwards when disabled", func(t *testing.T) {
		s := newTestShim(t, http.StatusOK, "{}", nil)
		w := serveHandler(t, "PUT", "/resource_providers/{uuid}/traits",
			s.HandleUpdateResourceProviderTraits,
			"/resource_providers/not-a-uuid/traits")
		if w.Code != http.StatusOK {
			t.Fatalf("status = %d, want %d", w.Code, http.StatusOK)
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
	t.Run("invalid uuid forwards when disabled", func(t *testing.T) {
		s := newTestShim(t, http.StatusOK, "{}", nil)
		w := serveHandler(t, "DELETE", "/resource_providers/{uuid}/traits",
			s.HandleDeleteResourceProviderTraits,
			"/resource_providers/not-a-uuid/traits")
		if w.Code != http.StatusOK {
			t.Fatalf("status = %d, want %d", w.Code, http.StatusOK)
		}
	})
}

func TestHandleResourceProviderTraits_Enabled(t *testing.T) {
	down, up := newTestTimers()
	s := &Shim{
		config: config{
			PlacementURL: "http://should-not-be-called:1234",
			Features:     featuresConfig{ResourceProviderTraits: true},
		},
		maxBodyLogSize:         4096,
		downstreamRequestTimer: down,
		upstreamRequestTimer:   up,
	}
	t.Run("GET returns 501", func(t *testing.T) {
		w := serveHandler(t, "GET", "/resource_providers/{uuid}/traits",
			s.HandleListResourceProviderTraits,
			"/resource_providers/"+validUUID+"/traits")
		if w.Code != http.StatusNotImplemented {
			t.Fatalf("status = %d, want %d", w.Code, http.StatusNotImplemented)
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
	t.Run("invalid uuid returns 400", func(t *testing.T) {
		w := serveHandler(t, "GET", "/resource_providers/{uuid}/traits",
			s.HandleListResourceProviderTraits,
			"/resource_providers/not-a-uuid/traits")
		if w.Code != http.StatusBadRequest {
			t.Fatalf("status = %d, want %d", w.Code, http.StatusBadRequest)
		}
	})
}
