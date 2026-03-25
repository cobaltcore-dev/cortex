// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package commitments

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/sapcc/go-api-declarations/liquid"
)

// HandleReportUsage implements POST /commitments/v1/projects/:project_id/report-usage from Limes LIQUID API.
// See: https://github.com/sapcc/go-api-declarations/blob/main/liquid/report_usage.go
// See: https://pkg.go.dev/github.com/sapcc/go-api-declarations/liquid
//
// This endpoint reports usage information for a specific project's committed resources,
// including per-AZ usage, physical usage, and detailed VM subresources.
func (api *HTTPAPI) HandleReportUsage(w http.ResponseWriter, r *http.Request) {
	startTime := time.Now()
	statusCode := http.StatusOK

	// Extract or generate request ID for tracing - always set in response header
	requestID := r.Header.Get("X-Request-ID")
	if requestID == "" {
		requestID = uuid.New().String()
	}
	w.Header().Set("X-Request-ID", requestID)

	log := baseLog.WithValues("requestID", requestID, "endpoint", "report-usage")

	// Check if API is enabled
	if !api.config.EnableReportUsageAPI {
		statusCode = http.StatusServiceUnavailable
		log.Info("report-usage API is disabled, rejecting request")
		http.Error(w, "report-usage API is disabled", statusCode)
		api.recordUsageMetrics(statusCode, startTime)
		return
	}

	if r.Method != http.MethodPost {
		statusCode = http.StatusMethodNotAllowed
		http.Error(w, "Method not allowed", statusCode)
		api.recordUsageMetrics(statusCode, startTime)
		return
	}

	// Extract project UUID from URL path
	// URL pattern: /commitments/v1/projects/:project_id/report-usage
	projectID, err := extractProjectIDFromPath(r.URL.Path)
	if err != nil {
		log.Error(err, "failed to extract project ID from path")
		statusCode = http.StatusBadRequest
		http.Error(w, "Invalid URL path: "+err.Error(), statusCode)
		api.recordUsageMetrics(statusCode, startTime)
		return
	}

	// Parse request body
	var req liquid.ServiceUsageRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		log.Error(err, "failed to decode request body")
		statusCode = http.StatusBadRequest
		http.Error(w, "Invalid request body: "+err.Error(), statusCode)
		api.recordUsageMetrics(statusCode, startTime)
		return
	}

	// Use UsageCalculator to build usage report
	calculator := NewUsageCalculator(api.client, api.novaClient)
	report, err := calculator.CalculateUsage(r.Context(), log, projectID, req.AllAZs)
	if err != nil {
		log.Error(err, "failed to calculate usage report", "projectID", projectID)
		statusCode = http.StatusInternalServerError
		http.Error(w, "Failed to generate usage report: "+err.Error(), statusCode)
		api.recordUsageMetrics(statusCode, startTime)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(statusCode)
	if err := json.NewEncoder(w).Encode(report); err != nil {
		log.Error(err, "failed to encode usage report")
	}
	api.recordUsageMetrics(statusCode, startTime)
}

// recordUsageMetrics records Prometheus metrics for a report-usage request.
func (api *HTTPAPI) recordUsageMetrics(statusCode int, startTime time.Time) {
	duration := time.Since(startTime).Seconds()
	statusCodeStr := strconv.Itoa(statusCode)
	api.usageMonitor.requestCounter.WithLabelValues(statusCodeStr).Inc()
	api.usageMonitor.requestDuration.WithLabelValues(statusCodeStr).Observe(duration)
}

// extractProjectIDFromPath extracts the project UUID from the URL path.
// Expected path format: /commitments/v1/projects/:project_id/report-usage
func extractProjectIDFromPath(path string) (string, error) {
	// Path: /commitments/v1/projects/<uuid>/report-usage
	parts := strings.Split(strings.Trim(path, "/"), "/")
	// Expected: ["v1", "commitments", "projects", "<uuid>", "report-usage"]
	if len(parts) < 5 {
		return "", fmt.Errorf("path too short: %s", path)
	}
	if parts[2] != "projects" || parts[4] != "report-usage" {
		return "", fmt.Errorf("unexpected path format: %s", path)
	}
	projectID := parts[3]
	if projectID == "" {
		return "", fmt.Errorf("empty project ID in path: %s", path)
	}
	return projectID, nil
}
