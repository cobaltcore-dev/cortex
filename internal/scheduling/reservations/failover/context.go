// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package failover

import (
	"context"

	"github.com/cobaltcore-dev/cortex/internal/scheduling/reservations"
	"github.com/go-logr/logr"
	"github.com/google/uuid"
)

// WithNewGlobalRequestID creates a new context with a failover-prefixed global request ID.
func WithNewGlobalRequestID(ctx context.Context) context.Context {
	return reservations.WithGlobalRequestID(ctx, "failover-"+uuid.New().String())
}

// LoggerFromContext returns a logger with greq and req values from the context.
// This creates a child logger with the request tracking values pre-attached,
// so you don't need to repeat them in every log call.
func LoggerFromContext(ctx context.Context) logr.Logger {
	return log.WithValues(
		"greq", reservations.GlobalRequestIDFromContext(ctx),
		"req", reservations.RequestIDFromContext(ctx),
	)
}
