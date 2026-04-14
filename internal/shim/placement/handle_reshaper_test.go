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
