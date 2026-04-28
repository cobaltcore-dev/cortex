// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package placement

import (
	"net/http"
	"testing"
)

func TestHandleListUsages(t *testing.T) {
	var gotPath string
	s := newTestShim(t, http.StatusOK, `{"usages":{}}`, &gotPath)
	w := serveHandler(t, "GET", "/usages", s.HandleListUsages, "/usages")
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusOK)
	}
	if gotPath != "/usages" {
		t.Fatalf("upstream path = %q, want /usages", gotPath)
	}
}

func TestHandleUsages_HybridMode(t *testing.T) {
	down, up := newTestTimers()
	s := &Shim{
		config: config{
			PlacementURL: "http://should-not-be-called:1234",
			Features:     featuresConfig{Usages: FeatureModeHybrid},
		},
		maxBodyLogSize:         4096,
		downstreamRequestTimer: down,
		upstreamRequestTimer:   up,
	}
	t.Run("GET returns 501", func(t *testing.T) {
		w := serveHandler(t, "GET", "/usages",
			s.HandleListUsages, "/usages")
		if w.Code != http.StatusNotImplemented {
			t.Fatalf("status = %d, want %d", w.Code, http.StatusNotImplemented)
		}
	})
}

func TestHandleUsages_CRDMode(t *testing.T) {
	down, up := newTestTimers()
	s := &Shim{
		config: config{
			PlacementURL: "http://should-not-be-called:1234",
			Features:     featuresConfig{Usages: FeatureModeCRD},
		},
		maxBodyLogSize:         4096,
		downstreamRequestTimer: down,
		upstreamRequestTimer:   up,
	}
	t.Run("GET returns 501", func(t *testing.T) {
		w := serveHandler(t, "GET", "/usages",
			s.HandleListUsages, "/usages")
		if w.Code != http.StatusNotImplemented {
			t.Fatalf("status = %d, want %d", w.Code, http.StatusNotImplemented)
		}
	})
}
