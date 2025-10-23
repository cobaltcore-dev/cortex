// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package controller

const (
	DefaultTTLAfterDecisionSeconds = 24 * 60 * 60 // 24 hours in seconds
)

// Configuration for the decisions operator.
type Config struct {
	// TTL for scheduling decisions after the last decision's RequestedAt timestamp (in seconds)
	TTLAfterDecisionSeconds int `json:"ttlAfterDecisionSeconds,omitempty"`
}
