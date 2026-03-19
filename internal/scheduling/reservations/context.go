// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package reservations

import "context"

// ContextKey is the type for context keys used in request tracking.
type ContextKey string

const (
	// GlobalRequestIDKey is the context key for the global request ID.
	GlobalRequestIDKey ContextKey = "globalRequestID"
	// RequestIDKey is the context key for the request ID.
	RequestIDKey ContextKey = "requestID"
)

// WithGlobalRequestID returns a new context with the global request ID set.
// GlobalRequestID identifies the overall reconciliation context.
func WithGlobalRequestID(ctx context.Context, id string) context.Context {
	return context.WithValue(ctx, GlobalRequestIDKey, id)
}

// WithRequestID returns a new context with the request ID set.
// RequestID identifies the specific item being processed (typically VM UUID).
func WithRequestID(ctx context.Context, id string) context.Context {
	return context.WithValue(ctx, RequestIDKey, id)
}

// GlobalRequestIDFromContext retrieves the global request ID from the context.
// Returns empty string if not set.
func GlobalRequestIDFromContext(ctx context.Context) string {
	if v := ctx.Value(GlobalRequestIDKey); v != nil {
		if s, ok := v.(string); ok {
			return s
		}
	}
	return ""
}

// RequestIDFromContext retrieves the request ID from the context.
// Returns empty string if not set.
func RequestIDFromContext(ctx context.Context) string {
	if v := ctx.Value(RequestIDKey); v != nil {
		if s, ok := v.(string); ok {
			return s
		}
	}
	return ""
}
