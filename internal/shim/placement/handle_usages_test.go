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
