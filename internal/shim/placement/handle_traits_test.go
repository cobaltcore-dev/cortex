// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package placement

import (
	"net/http"
	"testing"
)

func TestHandleListTraits(t *testing.T) {
	var gotPath string
	s := newTestShim(t, http.StatusOK, `{"traits":[]}`, &gotPath)
	w := serveHandler(t, "GET", "/traits", s.HandleListTraits, "/traits")
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusOK)
	}
	if gotPath != "/traits" {
		t.Fatalf("upstream path = %q, want /traits", gotPath)
	}
}

func TestHandleShowTrait(t *testing.T) {
	s := newTestShim(t, http.StatusNoContent, "", nil)
	w := serveHandler(t, "GET", "/traits/{name}", s.HandleShowTrait, "/traits/HW_CPU_X86_AVX2")
	if w.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusNoContent)
	}
}

func TestHandleUpdateTrait(t *testing.T) {
	s := newTestShim(t, http.StatusCreated, "", nil)
	w := serveHandler(t, "PUT", "/traits/{name}", s.HandleUpdateTrait, "/traits/CUSTOM_TRAIT")
	if w.Code != http.StatusCreated {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusCreated)
	}
}

func TestHandleDeleteTrait(t *testing.T) {
	s := newTestShim(t, http.StatusNoContent, "", nil)
	w := serveHandler(t, "DELETE", "/traits/{name}", s.HandleDeleteTrait, "/traits/CUSTOM_TRAIT")
	if w.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusNoContent)
	}
}

func TestHandleTraits_Enabled(t *testing.T) {
	down, up := newTestTimers()
	s := &Shim{
		config: config{
			PlacementURL: "http://should-not-be-called:1234",
			Features:     featuresConfig{Traits: true},
		},
		maxBodyLogSize:         4096,
		downstreamRequestTimer: down,
		upstreamRequestTimer:   up,
	}
	t.Run("GET list returns 501", func(t *testing.T) {
		w := serveHandler(t, "GET", "/traits", s.HandleListTraits, "/traits")
		if w.Code != http.StatusNotImplemented {
			t.Fatalf("status = %d, want %d", w.Code, http.StatusNotImplemented)
		}
	})
	t.Run("GET show returns 501", func(t *testing.T) {
		w := serveHandler(t, "GET", "/traits/{name}", s.HandleShowTrait, "/traits/CUSTOM_T")
		if w.Code != http.StatusNotImplemented {
			t.Fatalf("status = %d, want %d", w.Code, http.StatusNotImplemented)
		}
	})
	t.Run("PUT returns 501", func(t *testing.T) {
		w := serveHandler(t, "PUT", "/traits/{name}", s.HandleUpdateTrait, "/traits/CUSTOM_T")
		if w.Code != http.StatusNotImplemented {
			t.Fatalf("status = %d, want %d", w.Code, http.StatusNotImplemented)
		}
	})
	t.Run("DELETE returns 501", func(t *testing.T) {
		w := serveHandler(t, "DELETE", "/traits/{name}", s.HandleDeleteTrait, "/traits/CUSTOM_T")
		if w.Code != http.StatusNotImplemented {
			t.Fatalf("status = %d, want %d", w.Code, http.StatusNotImplemented)
		}
	})
}
