// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package commitments

import (
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/sapcc/go-api-declarations/liquid"
)

// handles POST /v1/report-capacity requests from Limes:
// See: https://github.com/sapcc/go-api-declarations/blob/main/liquid/commitment.go
// See: https://pkg.go.dev/github.com/sapcc/go-api-declarations/liquid
// Reports available capacity across all flavor group resources. Note, unit is specified in the Info API response with multiple of the smallest memory resource unit within a flavor group.
func (api *HTTPAPI) HandleReportCapacity(w http.ResponseWriter, r *http.Request) {
	// Extract or generate request ID for tracing
	requestID := r.Header.Get("X-Request-ID")
	if requestID == "" {
		requestID = fmt.Sprintf("req-%d", time.Now().UnixNano())
	}
	log := commitmentApiLog.WithValues("requestID", requestID, "endpoint", "/v1/report-capacity")

	// Only accept POST method
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	log.V(1).Info("processing report capacity request")

	// Parse request body (may be empty or contain ServiceCapacityRequest)
	var req liquid.ServiceCapacityRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		// Empty body is acceptable for capacity reports
		req = liquid.ServiceCapacityRequest{}
	}

	// Calculate capacity
	calculator := NewCapacityCalculator(api.client)
	report, err := calculator.CalculateCapacity(r.Context())
	if err != nil {
		log.Error(err, "failed to calculate capacity")
		http.Error(w, "Failed to calculate capacity: "+err.Error(),
			http.StatusInternalServerError)
		return
	}

	log.Info("calculated capacity report", "resourceCount", len(report.Resources))

	// Return response
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	if err := json.NewEncoder(w).Encode(report); err != nil {
		log.Error(err, "failed to encode capacity report")
		return
	}
}
