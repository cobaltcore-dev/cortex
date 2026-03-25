// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package commitments

import (
	"net/http"

	"github.com/google/uuid"
)

// HandleQuota implements PUT /commitments/v1/projects/:project_id/quota from Limes LIQUID API.
// See: https://pkg.go.dev/github.com/sapcc/go-api-declarations/liquid
//
// This is a no-op endpoint that accepts quota requests but doesn't store them.
// Cortex does not enforce quotas for committed resources - quota enforcement
// happens through commitment validation at change-commitments time.
// The endpoint exists for API compatibility with the LIQUID specification.
func (api *HTTPAPI) HandleQuota(w http.ResponseWriter, r *http.Request) {
	// Extract or generate request ID for tracing
	requestID := r.Header.Get("X-Request-ID")
	if requestID == "" {
		requestID = uuid.New().String()
	}
	w.Header().Set("X-Request-ID", requestID)

	log := baseLog.WithValues("requestID", requestID, "endpoint", "quota")

	if r.Method != http.MethodPut {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// No-op: Accept the quota request but don't store it
	// Cortex handles capacity through commitments, not quotas
	log.V(1).Info("received quota request (no-op)", "path", r.URL.Path)

	// Return 204 No Content as expected by the LIQUID API
	w.WriteHeader(http.StatusNoContent)
}
