// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package controller

import "time"

const (
	DefaultTTLHoursAfterDecision = 24 * time.Hour
)

// Configuration for the decisions operator.
type Config struct {
	// TTL for scheduling decisions after the last decision's RequestedAt timestamp
	TTLHoursAfterDecision time.Duration `json:"ttlHoursAfterDecision,omitempty"`
}
