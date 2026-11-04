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
var baseLog = ctrl.Log.WithName("committed-resource").WithValues("module", "committed-resources")

// WithNewReconcileID creates a new context with a unique per-reconcile correlation ID.
// Use this at the entry point of each Reconcile call so every log line in that run
// carries the same greq, making a single reconcile easy to isolate in logs.
func WithNewReconcileID(ctx context.Context) context.Context {
	return reservations.WithGlobalRequestID(ctx, "reconcile-"+uuid.New().String())
}

// LoggerFromContext returns a logger with greq and req values from the context.
func LoggerFromContext(ctx context.Context) logr.Logger {
	return baseLog.WithValues(
		"greq", reservations.GlobalRequestIDFromContext(ctx),
		"req", reservations.RequestIDFromContext(ctx),
	)
}
