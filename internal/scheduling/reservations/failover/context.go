// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package failover

import (
	"context"

	"github.com/cobaltcore-dev/cortex/internal/scheduling/reservations"
	"github.com/go-logr/logr"
)

// LoggerFromContext returns a logger with greq and req values from the context.
// This creates a child logger with the request tracking values pre-attached,
// so you don't need to repeat them in every log call.
func LoggerFromContext(ctx context.Context) logr.Logger {
	return log.WithValues(
		"greq", reservations.GlobalRequestIDFromContext(ctx),
		"req", reservations.RequestIDFromContext(ctx),
	)
}
