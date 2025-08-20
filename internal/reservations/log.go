// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package reservations

import (
	"context"
	"log/slog"

	"github.com/go-logr/logr"
)

// Conform to the LogSink interface for the controller-runtime logger.
type SlogLogSink struct{ log *slog.Logger }

func (s SlogLogSink) Init(info logr.RuntimeInfo) {}

func (s SlogLogSink) Enabled(level int) bool {
	return s.log.Enabled(context.Background(), slog.Level(level))
}

func (s SlogLogSink) Info(level int, msg string, keysAndValues ...any) {
	s.log.Info(msg, keysAndValues...)
}

func (s SlogLogSink) Error(err error, msg string, keysAndValues ...any) {
	s.log.Error(msg, append(keysAndValues, slog.Any("error", err))...)
}

func (s SlogLogSink) WithValues(keysAndValues ...any) logr.LogSink {
	return SlogLogSink{log: s.log.With(keysAndValues...)}
}

func (s SlogLogSink) WithName(name string) logr.LogSink {
	return SlogLogSink{log: s.log.With(slog.String("name", name))}
}
