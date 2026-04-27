// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package api

import (
	"strconv"
	"time"

	"github.com/sapcc/go-api-declarations/liquid"
)

// recordMetrics records Prometheus metrics for a change commitments request.
func (api *HTTPAPI) recordMetrics(req liquid.CommitmentChangeRequest, resp liquid.CommitmentChangeResponse, statusCode int, startTime time.Time) {
	duration := time.Since(startTime).Seconds()
	statusCodeStr := strconv.Itoa(statusCode)

	// Record request counter and duration
	api.monitor.requestCounter.WithLabelValues(statusCodeStr).Inc()
	api.monitor.requestDuration.WithLabelValues(statusCodeStr).Observe(duration)

	// Count total commitment changes in the request
	commitmentCount := countCommitments(req)

	// Determine result based on response
	result := "success"
	if resp.RejectionReason != "" {
		result = "rejected"
	}

	// Record commitment changes counter
	api.monitor.commitmentChanges.WithLabelValues(result).Add(float64(commitmentCount))
}

// countCommitments counts the total number of commitments in a request.
func countCommitments(req liquid.CommitmentChangeRequest) int {
	count := 0
	for _, projectChanges := range req.ByProject {
		for _, resourceChanges := range projectChanges.ByResource {
			count += len(resourceChanges.Commitments)
		}
	}
	return count
}
