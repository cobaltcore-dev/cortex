// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package placement

import (
	"net/http"
	"testing"
)

func TestHandleListResourceClasses(t *testing.T) {
	var gotPath string
	s := newTestShim(t, http.StatusOK, `{"resource_classes":[]}`, &gotPath)
	w := serveHandler(t, "GET", "/resource_classes", s.HandleListResourceClasses, "/resource_classes")
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusOK)
	}
	if gotPath != "/resource_classes" {
		t.Fatalf("upstream path = %q, want /resource_classes", gotPath)
	}
}

func TestHandleCreateResourceClass(t *testing.T) {
	s := newTestShim(t, http.StatusCreated, "{}", nil)
	w := serveHandler(t, "POST", "/resource_classes", s.HandleCreateResourceClass, "/resource_classes")
	if w.Code != http.StatusCreated {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusCreated)
	}
}

func TestHandleShowResourceClass(t *testing.T) {
	var gotPath string
	s := newTestShim(t, http.StatusOK, "{}", &gotPath)
	w := serveHandler(t, "GET", "/resource_classes/{name}", s.HandleShowResourceClass, "/resource_classes/VCPU")
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusOK)
	}
	if gotPath != "/resource_classes/VCPU" {
		t.Fatalf("upstream path = %q, want /resource_classes/VCPU", gotPath)
	}
}

func TestHandleUpdateResourceClass(t *testing.T) {
	s := newTestShim(t, http.StatusNoContent, "", nil)
	w := serveHandler(t, "PUT", "/resource_classes/{name}", s.HandleUpdateResourceClass, "/resource_classes/CUSTOM_FOO")
	if w.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusNoContent)
	}
}

func TestHandleDeleteResourceClass(t *testing.T) {
	s := newTestShim(t, http.StatusNoContent, "", nil)
	w := serveHandler(t, "DELETE", "/resource_classes/{name}", s.HandleDeleteResourceClass, "/resource_classes/CUSTOM_BAR")
	if w.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusNoContent)
	}
}
