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
