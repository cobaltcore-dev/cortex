// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package placement

import (
	"net/http"
	"testing"
)

func TestHandleListResourceProviders(t *testing.T) {
	var gotPath string
	s := newTestShim(t, http.StatusOK, `{"resource_providers":[]}`, &gotPath)
	w := serveHandler(t, "GET", "/resource_providers", s.HandleListResourceProviders, "/resource_providers")
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusOK)
	}
	if gotPath != "/resource_providers" {
		t.Fatalf("upstream path = %q, want /resource_providers", gotPath)
	}
}

func TestHandleCreateResourceProvider(t *testing.T) {
	s := newTestShim(t, http.StatusCreated, "{}", nil)
	w := serveHandler(t, "POST", "/resource_providers", s.HandleCreateResourceProvider, "/resource_providers")
	if w.Code != http.StatusCreated {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusCreated)
	}
}

func TestHandleShowResourceProvider(t *testing.T) {
	t.Run("valid uuid", func(t *testing.T) {
		s := newTestShim(t, http.StatusOK, "{}", nil)
		w := serveHandler(t, "GET", "/resource_providers/{uuid}", s.HandleShowResourceProvider,
			"/resource_providers/"+validUUID)
		if w.Code != http.StatusOK {
			t.Fatalf("status = %d, want %d", w.Code, http.StatusOK)
		}
	})
	t.Run("invalid uuid", func(t *testing.T) {
		s := newTestShim(t, http.StatusOK, "{}", nil)
		w := serveHandler(t, "GET", "/resource_providers/{uuid}", s.HandleShowResourceProvider,
			"/resource_providers/not-a-uuid")
		if w.Code != http.StatusBadRequest {
			t.Fatalf("status = %d, want %d", w.Code, http.StatusBadRequest)
		}
	})
}

func TestHandleUpdateResourceProvider(t *testing.T) {
	t.Run("valid uuid", func(t *testing.T) {
		s := newTestShim(t, http.StatusOK, "{}", nil)
		w := serveHandler(t, "PUT", "/resource_providers/{uuid}", s.HandleUpdateResourceProvider,
			"/resource_providers/"+validUUID)
		if w.Code != http.StatusOK {
			t.Fatalf("status = %d, want %d", w.Code, http.StatusOK)
		}
	})
	t.Run("invalid uuid", func(t *testing.T) {
		s := newTestShim(t, http.StatusOK, "{}", nil)
		w := serveHandler(t, "PUT", "/resource_providers/{uuid}", s.HandleUpdateResourceProvider,
			"/resource_providers/not-a-uuid")
		if w.Code != http.StatusBadRequest {
			t.Fatalf("status = %d, want %d", w.Code, http.StatusBadRequest)
		}
	})
}

func TestHandleDeleteResourceProvider(t *testing.T) {
	t.Run("valid uuid", func(t *testing.T) {
		s := newTestShim(t, http.StatusNoContent, "", nil)
		w := serveHandler(t, "DELETE", "/resource_providers/{uuid}", s.HandleDeleteResourceProvider,
			"/resource_providers/"+validUUID)
		if w.Code != http.StatusNoContent {
			t.Fatalf("status = %d, want %d", w.Code, http.StatusNoContent)
		}
	})
	t.Run("invalid uuid", func(t *testing.T) {
		s := newTestShim(t, http.StatusOK, "{}", nil)
		w := serveHandler(t, "DELETE", "/resource_providers/{uuid}", s.HandleDeleteResourceProvider,
			"/resource_providers/not-a-uuid")
		if w.Code != http.StatusBadRequest {
			t.Fatalf("status = %d, want %d", w.Code, http.StatusBadRequest)
		}
	})
}
