// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package placement

import (
	"net/http"
	"testing"
)

func TestHandleListResourceProviderInventories(t *testing.T) {
	t.Run("valid uuid", func(t *testing.T) {
		s := newTestShim(t, http.StatusOK, "{}", nil)
		w := serveHandler(t, "GET", "/resource_providers/{uuid}/inventories",
			s.HandleListResourceProviderInventories,
			"/resource_providers/"+validUUID+"/inventories")
		if w.Code != http.StatusOK {
			t.Fatalf("status = %d, want %d", w.Code, http.StatusOK)
		}
	})
	t.Run("invalid uuid", func(t *testing.T) {
		s := newTestShim(t, http.StatusOK, "{}", nil)
		w := serveHandler(t, "GET", "/resource_providers/{uuid}/inventories",
			s.HandleListResourceProviderInventories,
			"/resource_providers/not-a-uuid/inventories")
		if w.Code != http.StatusBadRequest {
			t.Fatalf("status = %d, want %d", w.Code, http.StatusBadRequest)
		}
	})
}

func TestHandleUpdateResourceProviderInventories(t *testing.T) {
	t.Run("valid uuid", func(t *testing.T) {
		s := newTestShim(t, http.StatusOK, "{}", nil)
		w := serveHandler(t, "PUT", "/resource_providers/{uuid}/inventories",
			s.HandleUpdateResourceProviderInventories,
			"/resource_providers/"+validUUID+"/inventories")
		if w.Code != http.StatusOK {
			t.Fatalf("status = %d, want %d", w.Code, http.StatusOK)
		}
	})
	t.Run("invalid uuid", func(t *testing.T) {
		s := newTestShim(t, http.StatusOK, "{}", nil)
		w := serveHandler(t, "PUT", "/resource_providers/{uuid}/inventories",
			s.HandleUpdateResourceProviderInventories,
			"/resource_providers/not-a-uuid/inventories")
		if w.Code != http.StatusBadRequest {
			t.Fatalf("status = %d, want %d", w.Code, http.StatusBadRequest)
		}
	})
}

func TestHandleDeleteResourceProviderInventories(t *testing.T) {
	t.Run("valid uuid", func(t *testing.T) {
		s := newTestShim(t, http.StatusNoContent, "", nil)
		w := serveHandler(t, "DELETE", "/resource_providers/{uuid}/inventories",
			s.HandleDeleteResourceProviderInventories,
			"/resource_providers/"+validUUID+"/inventories")
		if w.Code != http.StatusNoContent {
			t.Fatalf("status = %d, want %d", w.Code, http.StatusNoContent)
		}
	})
	t.Run("invalid uuid", func(t *testing.T) {
		s := newTestShim(t, http.StatusOK, "{}", nil)
		w := serveHandler(t, "DELETE", "/resource_providers/{uuid}/inventories",
			s.HandleDeleteResourceProviderInventories,
			"/resource_providers/not-a-uuid/inventories")
		if w.Code != http.StatusBadRequest {
			t.Fatalf("status = %d, want %d", w.Code, http.StatusBadRequest)
		}
	})
}

func TestHandleShowResourceProviderInventory(t *testing.T) {
	t.Run("valid", func(t *testing.T) {
		var gotPath string
		s := newTestShim(t, http.StatusOK, "{}", &gotPath)
		path := "/resource_providers/" + validUUID + "/inventories/VCPU"
		w := serveHandler(t, "GET", "/resource_providers/{uuid}/inventories/{resource_class}",
			s.HandleShowResourceProviderInventory, path)
		if w.Code != http.StatusOK {
			t.Fatalf("status = %d, want %d", w.Code, http.StatusOK)
		}
		if gotPath != path {
			t.Fatalf("upstream path = %q, want %q", gotPath, path)
		}
	})
	t.Run("invalid uuid", func(t *testing.T) {
		s := newTestShim(t, http.StatusOK, "{}", nil)
		w := serveHandler(t, "GET", "/resource_providers/{uuid}/inventories/{resource_class}",
			s.HandleShowResourceProviderInventory,
			"/resource_providers/not-a-uuid/inventories/VCPU")
		if w.Code != http.StatusBadRequest {
			t.Fatalf("status = %d, want %d", w.Code, http.StatusBadRequest)
		}
	})
}

func TestHandleUpdateResourceProviderInventory(t *testing.T) {
	t.Run("valid", func(t *testing.T) {
		s := newTestShim(t, http.StatusOK, "{}", nil)
		w := serveHandler(t, "PUT", "/resource_providers/{uuid}/inventories/{resource_class}",
			s.HandleUpdateResourceProviderInventory,
			"/resource_providers/"+validUUID+"/inventories/VCPU")
		if w.Code != http.StatusOK {
			t.Fatalf("status = %d, want %d", w.Code, http.StatusOK)
		}
	})
	t.Run("invalid uuid", func(t *testing.T) {
		s := newTestShim(t, http.StatusOK, "{}", nil)
		w := serveHandler(t, "PUT", "/resource_providers/{uuid}/inventories/{resource_class}",
			s.HandleUpdateResourceProviderInventory,
			"/resource_providers/not-a-uuid/inventories/VCPU")
		if w.Code != http.StatusBadRequest {
			t.Fatalf("status = %d, want %d", w.Code, http.StatusBadRequest)
		}
	})
}

func TestHandleDeleteResourceProviderInventory(t *testing.T) {
	t.Run("valid", func(t *testing.T) {
		s := newTestShim(t, http.StatusNoContent, "", nil)
		w := serveHandler(t, "DELETE", "/resource_providers/{uuid}/inventories/{resource_class}",
			s.HandleDeleteResourceProviderInventory,
			"/resource_providers/"+validUUID+"/inventories/VCPU")
		if w.Code != http.StatusNoContent {
			t.Fatalf("status = %d, want %d", w.Code, http.StatusNoContent)
		}
	})
	t.Run("invalid uuid", func(t *testing.T) {
		s := newTestShim(t, http.StatusOK, "{}", nil)
		w := serveHandler(t, "DELETE", "/resource_providers/{uuid}/inventories/{resource_class}",
			s.HandleDeleteResourceProviderInventory,
			"/resource_providers/not-a-uuid/inventories/VCPU")
		if w.Code != http.StatusBadRequest {
			t.Fatalf("status = %d, want %d", w.Code, http.StatusBadRequest)
		}
	})
}

func TestHandleResourceProviderInventories_HybridMode(t *testing.T) {
	down, up := newTestTimers()
	s := &Shim{
		config: config{
			PlacementURL: "http://should-not-be-called:1234",
			Features:     featuresConfig{Inventories: FeatureModeHybrid},
		},
		maxBodyLogSize:         4096,
		downstreamRequestTimer: down,
		upstreamRequestTimer:   up,
	}
	t.Run("GET list returns 501", func(t *testing.T) {
		w := serveHandler(t, "GET", "/resource_providers/{uuid}/inventories",
			s.HandleListResourceProviderInventories,
			"/resource_providers/"+validUUID+"/inventories")
		if w.Code != http.StatusNotImplemented {
			t.Fatalf("status = %d, want %d", w.Code, http.StatusNotImplemented)
		}
	})
	t.Run("PUT list returns 501", func(t *testing.T) {
		w := serveHandler(t, "PUT", "/resource_providers/{uuid}/inventories",
			s.HandleUpdateResourceProviderInventories,
			"/resource_providers/"+validUUID+"/inventories")
		if w.Code != http.StatusNotImplemented {
			t.Fatalf("status = %d, want %d", w.Code, http.StatusNotImplemented)
		}
	})
	t.Run("DELETE list returns 501", func(t *testing.T) {
		w := serveHandler(t, "DELETE", "/resource_providers/{uuid}/inventories",
			s.HandleDeleteResourceProviderInventories,
			"/resource_providers/"+validUUID+"/inventories")
		if w.Code != http.StatusNotImplemented {
			t.Fatalf("status = %d, want %d", w.Code, http.StatusNotImplemented)
		}
	})
	t.Run("GET single returns 501", func(t *testing.T) {
		w := serveHandler(t, "GET", "/resource_providers/{uuid}/inventories/{resource_class}",
			s.HandleShowResourceProviderInventory,
			"/resource_providers/"+validUUID+"/inventories/VCPU")
		if w.Code != http.StatusNotImplemented {
			t.Fatalf("status = %d, want %d", w.Code, http.StatusNotImplemented)
		}
	})
	t.Run("PUT single returns 501", func(t *testing.T) {
		w := serveHandler(t, "PUT", "/resource_providers/{uuid}/inventories/{resource_class}",
			s.HandleUpdateResourceProviderInventory,
			"/resource_providers/"+validUUID+"/inventories/VCPU")
		if w.Code != http.StatusNotImplemented {
			t.Fatalf("status = %d, want %d", w.Code, http.StatusNotImplemented)
		}
	})
	t.Run("DELETE single returns 501", func(t *testing.T) {
		w := serveHandler(t, "DELETE", "/resource_providers/{uuid}/inventories/{resource_class}",
			s.HandleDeleteResourceProviderInventory,
			"/resource_providers/"+validUUID+"/inventories/VCPU")
		if w.Code != http.StatusNotImplemented {
			t.Fatalf("status = %d, want %d", w.Code, http.StatusNotImplemented)
		}
	})
}

func TestHandleResourceProviderInventories_CRDMode(t *testing.T) {
	down, up := newTestTimers()
	s := &Shim{
		config: config{
			PlacementURL: "http://should-not-be-called:1234",
			Features:     featuresConfig{Inventories: FeatureModeCRD},
		},
		maxBodyLogSize:         4096,
		downstreamRequestTimer: down,
		upstreamRequestTimer:   up,
	}
	t.Run("GET list returns 501", func(t *testing.T) {
		w := serveHandler(t, "GET", "/resource_providers/{uuid}/inventories",
			s.HandleListResourceProviderInventories,
			"/resource_providers/"+validUUID+"/inventories")
		if w.Code != http.StatusNotImplemented {
			t.Fatalf("status = %d, want %d", w.Code, http.StatusNotImplemented)
		}
	})
	t.Run("PUT list returns 501", func(t *testing.T) {
		w := serveHandler(t, "PUT", "/resource_providers/{uuid}/inventories",
			s.HandleUpdateResourceProviderInventories,
			"/resource_providers/"+validUUID+"/inventories")
		if w.Code != http.StatusNotImplemented {
			t.Fatalf("status = %d, want %d", w.Code, http.StatusNotImplemented)
		}
	})
	t.Run("DELETE list returns 501", func(t *testing.T) {
		w := serveHandler(t, "DELETE", "/resource_providers/{uuid}/inventories",
			s.HandleDeleteResourceProviderInventories,
			"/resource_providers/"+validUUID+"/inventories")
		if w.Code != http.StatusNotImplemented {
			t.Fatalf("status = %d, want %d", w.Code, http.StatusNotImplemented)
		}
	})
	t.Run("GET single returns 501", func(t *testing.T) {
		w := serveHandler(t, "GET", "/resource_providers/{uuid}/inventories/{resource_class}",
			s.HandleShowResourceProviderInventory,
			"/resource_providers/"+validUUID+"/inventories/VCPU")
		if w.Code != http.StatusNotImplemented {
			t.Fatalf("status = %d, want %d", w.Code, http.StatusNotImplemented)
		}
	})
	t.Run("PUT single returns 501", func(t *testing.T) {
		w := serveHandler(t, "PUT", "/resource_providers/{uuid}/inventories/{resource_class}",
			s.HandleUpdateResourceProviderInventory,
			"/resource_providers/"+validUUID+"/inventories/VCPU")
		if w.Code != http.StatusNotImplemented {
			t.Fatalf("status = %d, want %d", w.Code, http.StatusNotImplemented)
		}
	})
	t.Run("DELETE single returns 501", func(t *testing.T) {
		w := serveHandler(t, "DELETE", "/resource_providers/{uuid}/inventories/{resource_class}",
			s.HandleDeleteResourceProviderInventory,
			"/resource_providers/"+validUUID+"/inventories/VCPU")
		if w.Code != http.StatusNotImplemented {
			t.Fatalf("status = %d, want %d", w.Code, http.StatusNotImplemented)
		}
	})
}
