// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package commitments

import (
	"context"

	"github.com/cobaltcore-dev/cortex/internal/scheduling/reservations"
	"github.com/go-logr/logr"
	"github.com/google/uuid"
	ctrl "sigs.k8s.io/controller-runtime"
)

// baseLog is the base logger for all committed-resource operations.
// Use LoggerFromContext to get a logger with request tracking values.
var baseLog = ctrl.Log.WithName("committed-resource")

// WithNewGlobalRequestID creates a new context with a committed-resource-prefixed global request ID.
func WithNewGlobalRequestID(ctx context.Context) context.Context {
	return reservations.WithGlobalRequestID(ctx, "committed-resource-"+uuid.New().String())
}

// WithGlobalRequestID creates a new context with the specified global request ID.
// This is used to propagate existing request IDs (e.g., from the creator annotation).
func WithGlobalRequestID(ctx context.Context, greq string) context.Context {
	return reservations.WithGlobalRequestID(ctx, greq)
}

// LoggerFromContext returns a logger with greq and req values from the context.
// This creates a child logger with the request tracking values pre-attached,
// so you don't need to repeat them in every log call.
func LoggerFromContext(ctx context.Context) logr.Logger {
	return baseLog.WithValues(
		"greq", reservations.GlobalRequestIDFromContext(ctx),
		"req", reservations.RequestIDFromContext(ctx),
	)
}
