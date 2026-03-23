// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package commitments

import (
	"context"

	"github.com/cobaltcore-dev/cortex/internal/scheduling/reservations"
	"github.com/go-logr/logr"
	"github.com/google/uuid"
)

func WithNewGlobalRequestID(ctx context.Context) context.Context {
	return reservations.WithGlobalRequestID(ctx, "committed-resource-"+uuid.New().String())
}

func LoggerFromContext(ctx context.Context) logr.Logger {
	return commitmentLog.WithValues(
		"greq", reservations.GlobalRequestIDFromContext(ctx),
		"req", reservations.RequestIDFromContext(ctx),
	)
}

func APILoggerFromContext(ctx context.Context) logr.Logger {
	return apiLog.WithValues(
		"greq", reservations.GlobalRequestIDFromContext(ctx),
		"req", reservations.RequestIDFromContext(ctx),
	)
}
