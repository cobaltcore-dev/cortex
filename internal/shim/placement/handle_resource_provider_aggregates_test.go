// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package placement

import (
	"net/http"
	"testing"
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
