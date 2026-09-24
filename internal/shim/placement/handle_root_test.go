// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package placement

import (
	"net/http"
	"testing"
)

func TestHandleGetRoot(t *testing.T) {
	var gotPath string
	s := newTestShim(t, http.StatusOK, `{"versions":[]}`, &gotPath)
	w := serveHandler(t, "GET", "/{$}", s.HandleGetRoot, "/")
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusOK)
	}
	if gotPath != "/" {
		t.Fatalf("upstream path = %q, want %q", gotPath, "/")
	}
}

func TestHandleGetRoot_Enabled(t *testing.T) {
	down, up := newTestTimers()
	s := &Shim{
		config: config{
			PlacementURL: "http://should-not-be-called:1234",
			Features:     featuresConfig{Root: true},
		},
		maxBodyLogSize:         4096,
		downstreamRequestTimer: down,
		upstreamRequestTimer:   up,
	}
	w := serveHandler(t, "GET", "/{$}", s.HandleGetRoot, "/")
	if w.Code != http.StatusNotImplemented {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusNotImplemented)
	}
}
