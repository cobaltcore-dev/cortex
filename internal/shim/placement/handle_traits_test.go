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
	var gotPath string
	s := newTestShim(t, http.StatusNoContent, "", &gotPath)
	w := serveHandler(t, "GET", "/traits/{name}", s.HandleShowTrait, "/traits/HW_CPU_X86_AVX2")
	if w.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusNoContent)
	}
	if gotPath != "/traits/HW_CPU_X86_AVX2" {
		t.Fatalf("upstream path = %q, want /traits/HW_CPU_X86_AVX2", gotPath)
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
