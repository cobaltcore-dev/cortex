// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package controller

import "time"

// Configuration for the decisions operator.
type Config struct {
	// TTL for scheduling decisions after the last decision's RequestedAt timestamp
	// If not set, defaults to 14 days (336 hours)
	TTLHoursAfterDecision time.Duration `json:"ttlHoursAfterDecision,omitempty"`
}
