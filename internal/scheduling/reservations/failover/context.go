// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package failover

import (
	"context"

	"github.com/go-logr/logr"
)

// Context keys for request tracking
type contextKey string

const (
	globalRequestIDKey contextKey = "globalRequestID"
	requestIDKey       contextKey = "requestID"
)

// WithGlobalRequestID returns a new context with the global request ID set.
// GlobalRequestID identifies the overall reconciliation context (e.g., "periodic-123" or reservation name).
func WithGlobalRequestID(ctx context.Context, id string) context.Context {
	return context.WithValue(ctx, globalRequestIDKey, id)
}

// WithRequestID returns a new context with the request ID set.
// RequestID identifies the specific item being processed (typically VM UUID).
func WithRequestID(ctx context.Context, id string) context.Context {
	return context.WithValue(ctx, requestIDKey, id)
}

// GlobalRequestIDFromContext retrieves the global request ID from the context.
// Returns empty string if not set.
func GlobalRequestIDFromContext(ctx context.Context) string {
	if v := ctx.Value(globalRequestIDKey); v != nil {
		if s, ok := v.(string); ok {
			return s
		}
	}
	return ""
}

// RequestIDFromContext retrieves the request ID from the context.
// Returns empty string if not set.
func RequestIDFromContext(ctx context.Context) string {
	if v := ctx.Value(requestIDKey); v != nil {
		if s, ok := v.(string); ok {
			return s
		}
	}
	return ""
}

// LoggerFromContext returns a logger with greq and req values from the context.
// This creates a child logger with the request tracking values pre-attached,
// so you don't need to repeat them in every log call.
func LoggerFromContext(ctx context.Context) logr.Logger {
	return log.WithValues(
		"greq", GlobalRequestIDFromContext(ctx),
		"req", RequestIDFromContext(ctx),
	)
}
