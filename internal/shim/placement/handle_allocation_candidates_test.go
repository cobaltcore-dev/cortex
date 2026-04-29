// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package placement

import (
	"net/http"
	"testing"
)

func TestHandleListAllocationCandidates(t *testing.T) {
	var gotPath string
	s := newTestShim(t, http.StatusOK, `{"allocation_requests":[]}`, &gotPath)
	w := serveHandler(t, "GET", "/allocation_candidates", s.HandleListAllocationCandidates, "/allocation_candidates")
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusOK)
	}
	if gotPath != "/allocation_candidates" {
		t.Fatalf("upstream path = %q, want /allocation_candidates", gotPath)
	}
}

func TestHandleAllocationCandidates_HybridMode(t *testing.T) {
	down, up := newTestTimers()
	s := &Shim{
		config: config{
			PlacementURL: "http://should-not-be-called:1234",
			Features:     featuresConfig{AllocationCandidates: FeatureModeHybrid},
		},
		maxBodyLogSize:         4096,
		downstreamRequestTimer: down,
		upstreamRequestTimer:   up,
	}
	t.Run("GET returns 501", func(t *testing.T) {
		w := serveHandler(t, "GET", "/allocation_candidates",
			s.HandleListAllocationCandidates, "/allocation_candidates")
		if w.Code != http.StatusNotImplemented {
			t.Fatalf("status = %d, want %d", w.Code, http.StatusNotImplemented)
		}
	})
}

func TestHandleAllocationCandidates_CRDMode(t *testing.T) {
	down, up := newTestTimers()
	s := &Shim{
		config: config{
			PlacementURL: "http://should-not-be-called:1234",
			Features:     featuresConfig{AllocationCandidates: FeatureModeCRD},
		},
		maxBodyLogSize:         4096,
		downstreamRequestTimer: down,
		upstreamRequestTimer:   up,
	}
	t.Run("GET returns 501", func(t *testing.T) {
		w := serveHandler(t, "GET", "/allocation_candidates",
			s.HandleListAllocationCandidates, "/allocation_candidates")
		if w.Code != http.StatusNotImplemented {
			t.Fatalf("status = %d, want %d", w.Code, http.StatusNotImplemented)
		}
	})
}
