// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package api

import (
	"net/http"
	"strconv"
	"time"

	"github.com/sapcc/go-api-declarations/liquid"
)

// recordMetrics records Prometheus metrics for a change commitments request.
func (api *HTTPAPI) recordMetrics(req liquid.CommitmentChangeRequest, resp liquid.CommitmentChangeResponse, statusCode int, startTime time.Time) {
	duration := time.Since(startTime).Seconds()
	statusCodeStr := strconv.Itoa(statusCode)
	dryRunStr := strconv.FormatBool(req.DryRun)

	result := "accepted"
	if statusCode != http.StatusOK {
		result = "error"
	} else if resp.RejectionReason != "" {
		result = "rejected"
	}

	api.monitor.requestCounter.WithLabelValues(statusCodeStr, dryRunStr, result).Inc()
	api.monitor.requestDuration.WithLabelValues(statusCodeStr, dryRunStr).Observe(duration)

	commitmentCount := countCommitments(req)
	api.monitor.commitmentChanges.WithLabelValues(result, string(req.AZ), dryRunStr).Add(float64(commitmentCount))
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
