// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package placement

import (
	"net/http"
	"testing"
)

func TestHandlePostReshaper(t *testing.T) {
	var gotPath string
	s := newTestShim(t, http.StatusNoContent, "", &gotPath)
	w := serveHandler(t, "POST", "/reshaper", s.HandlePostReshaper, "/reshaper")
	if w.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusNoContent)
	}
	if gotPath != "/reshaper" {
		t.Fatalf("upstream path = %q, want /reshaper", gotPath)
	}
}

func TestHandleReshaper_HybridMode(t *testing.T) {
	down, up := newTestTimers()
	s := &Shim{
		config: config{
			PlacementURL: "http://should-not-be-called:1234",
			Features:     featuresConfig{Reshaper: FeatureModeHybrid},
		},
		maxBodyLogSize:         4096,
		downstreamRequestTimer: down,
		upstreamRequestTimer:   up,
	}
	t.Run("POST returns 501", func(t *testing.T) {
		w := serveHandler(t, "POST", "/reshaper",
			s.HandlePostReshaper, "/reshaper")
		if w.Code != http.StatusNotImplemented {
			t.Fatalf("status = %d, want %d", w.Code, http.StatusNotImplemented)
		}
	})
}

func TestHandleReshaper_CRDMode(t *testing.T) {
	down, up := newTestTimers()
	s := &Shim{
		config: config{
			PlacementURL: "http://should-not-be-called:1234",
			Features:     featuresConfig{Reshaper: FeatureModeCRD},
		},
		maxBodyLogSize:         4096,
		downstreamRequestTimer: down,
		upstreamRequestTimer:   up,
	}
	t.Run("POST returns 501", func(t *testing.T) {
		w := serveHandler(t, "POST", "/reshaper",
			s.HandlePostReshaper, "/reshaper")
		if w.Code != http.StatusNotImplemented {
			t.Fatalf("status = %d, want %d", w.Code, http.StatusNotImplemented)
		}
	})
}
