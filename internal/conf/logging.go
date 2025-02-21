// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package conf

import (
	"log/slog"
	"os"
)

// Conform to the slog.Leveler interface.
func (c LoggingConfig) Level() slog.Level {
	switch c.LevelStr {
	case "debug":
		return slog.LevelDebug
	case "info":
		return slog.LevelInfo
	case "warn":
		return slog.LevelWarn
	case "error":
		return slog.LevelError
	default:
		return slog.LevelInfo
	}
}

// Set the structured logger as given in the config.
func (c LoggingConfig) SetDefaultLogger() {
	opts := &slog.HandlerOptions{Level: c}
	var handler slog.Handler
	switch c.Format {
	case "json":
		handler = slog.NewJSONHandler(os.Stdout, opts)
	default:
		handler = slog.NewTextHandler(os.Stdout, opts)
	}
	slog.SetDefault(slog.New(handler))
	slog.Info("logging: set default logger", "level", c.LevelStr, "format", c.Format)
}
