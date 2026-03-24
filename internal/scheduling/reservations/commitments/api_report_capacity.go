// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package commitments

import (
	"encoding/json"
	"net/http"
	"strconv"
	"time"

	"github.com/sapcc/go-api-declarations/liquid"
)

// handles POST /v1/commitments/report-capacity requests from Limes:
// See: https://github.com/sapcc/go-api-declarations/blob/main/liquid/commitment.go
// See: https://pkg.go.dev/github.com/sapcc/go-api-declarations/liquid
// Reports available capacity across all flavor group resources. Note, unit is specified in the Info API response with multiple of the smallest memory resource unit within a flavor group.
func (api *HTTPAPI) HandleReportCapacity(w http.ResponseWriter, r *http.Request) {
	startTime := time.Now()
	statusCode := http.StatusOK

	ctx := WithNewGlobalRequestID(r.Context())
	logger := LoggerFromContext(ctx).WithValues("component", "api", "endpoint", "/v1/commitments/report-capacity")

	// Only accept POST method
	if r.Method != http.MethodPost {
		statusCode = http.StatusMethodNotAllowed
		http.Error(w, "Method not allowed", statusCode)
		api.recordCapacityMetrics(statusCode, startTime)
		return
	}

	logger.V(1).Info("processing report capacity request")

	// Parse request body (may be empty or contain ServiceCapacityRequest)
	var req liquid.ServiceCapacityRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		// Empty body is acceptable for capacity reports
		req = liquid.ServiceCapacityRequest{}
	}

	// Calculate capacity
	calculator := NewCapacityCalculator(api.client)
	report, err := calculator.CalculateCapacity(ctx)
	if err != nil {
		logger.Error(err, "failed to calculate capacity")
		statusCode = http.StatusInternalServerError
		http.Error(w, "Failed to calculate capacity: "+err.Error(), statusCode)
		api.recordCapacityMetrics(statusCode, startTime)
		return
	}

	logger.Info("calculated capacity report", "resourceCount", len(report.Resources))

	// Return response
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(statusCode)
	if err := json.NewEncoder(w).Encode(report); err != nil {
		logger.Error(err, "failed to encode capacity report")
	}
	api.recordCapacityMetrics(statusCode, startTime)
}

// recordCapacityMetrics records Prometheus metrics for a report-capacity request.
func (api *HTTPAPI) recordCapacityMetrics(statusCode int, startTime time.Time) {
	duration := time.Since(startTime).Seconds()
	statusCodeStr := strconv.Itoa(statusCode)
	api.capacityMonitor.requestCounter.WithLabelValues(statusCodeStr).Inc()
	api.capacityMonitor.requestDuration.WithLabelValues(statusCodeStr).Observe(duration)
}
