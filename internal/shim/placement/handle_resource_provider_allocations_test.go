// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package placement

import (
	"net/http"
	"testing"
)

func TestHandleListResourceProviderAllocations(t *testing.T) {
	t.Run("valid uuid", func(t *testing.T) {
		s := newTestShim(t, http.StatusOK, "{}", nil)
		w := serveHandler(t, "GET", "/resource_providers/{uuid}/allocations",
			s.HandleListResourceProviderAllocations,
			"/resource_providers/"+validUUID+"/allocations")
		if w.Code != http.StatusOK {
			t.Fatalf("status = %d, want %d", w.Code, http.StatusOK)
		}
	})
	t.Run("invalid uuid", func(t *testing.T) {
		s := newTestShim(t, http.StatusOK, "{}", nil)
		w := serveHandler(t, "GET", "/resource_providers/{uuid}/allocations",
			s.HandleListResourceProviderAllocations,
			"/resource_providers/not-a-uuid/allocations")
		if w.Code != http.StatusBadRequest {
			t.Fatalf("status = %d, want %d", w.Code, http.StatusBadRequest)
		}
	})
}

func TestHandleResourceProviderAllocations_HybridMode(t *testing.T) {
	down, up := newTestTimers()
	s := &Shim{
		config: config{
			PlacementURL: "http://should-not-be-called:1234",
			Features:     featuresConfig{Allocations: FeatureModeHybrid},
		},
		maxBodyLogSize:         4096,
		downstreamRequestTimer: down,
		upstreamRequestTimer:   up,
	}
	t.Run("GET returns 501", func(t *testing.T) {
		w := serveHandler(t, "GET", "/resource_providers/{uuid}/allocations",
			s.HandleListResourceProviderAllocations,
			"/resource_providers/"+validUUID+"/allocations")
		if w.Code != http.StatusNotImplemented {
			t.Fatalf("status = %d, want %d", w.Code, http.StatusNotImplemented)
		}
	})
}

func TestHandleResourceProviderAllocations_CRDMode(t *testing.T) {
	down, up := newTestTimers()
	s := &Shim{
		config: config{
			PlacementURL: "http://should-not-be-called:1234",
			Features:     featuresConfig{Allocations: FeatureModeCRD},
		},
		maxBodyLogSize:         4096,
		downstreamRequestTimer: down,
		upstreamRequestTimer:   up,
	}
	t.Run("GET returns 501", func(t *testing.T) {
		w := serveHandler(t, "GET", "/resource_providers/{uuid}/allocations",
			s.HandleListResourceProviderAllocations,
			"/resource_providers/"+validUUID+"/allocations")
		if w.Code != http.StatusNotImplemented {
			t.Fatalf("status = %d, want %d", w.Code, http.StatusNotImplemented)
		}
	})
}
