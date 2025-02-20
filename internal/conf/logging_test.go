// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package conf

import (
	"bytes"
	"io"
	"log/slog"
	"os"
	"strings"
	"testing"
)

func TestLoggingConfig_Level(t *testing.T) {
	tests := []struct {
		levelStr string
		expected slog.Level
	}{
		{"debug", slog.LevelDebug},
		{"info", slog.LevelInfo},
		{"warn", slog.LevelWarn},
		{"error", slog.LevelError},
		{"unknown", slog.LevelInfo}, // default case
	}

	for _, tt := range tests {
		t.Run(tt.levelStr, func(t *testing.T) {
			config := LoggingConfig{LevelStr: tt.levelStr}
			level := config.Level()
			if level != tt.expected {
				t.Errorf("expected %v, got %v", tt.expected, level)
			}
		})
	}
}

func TestLoggingConfig_SetDefaultLogger(t *testing.T) {
	tests := []struct {
		format   string
		expected string
	}{
		{"json", `"level":"INFO","msg":"logging: set default logger","level":"info","format":"json"`},
		{"text", `level=INFO msg="logging: set default logger" level=info format=text`},
	}

	for _, tt := range tests {
		t.Run(tt.format, func(t *testing.T) {
			config := LoggingConfig{LevelStr: "info", Format: tt.format}

			// Capture the output
			r, w, err := os.Pipe()
			if err != nil {
				t.Fatal(err)
			}
			stdout := os.Stdout
			defer func() { os.Stdout = stdout }()
			os.Stdout = w

			config.SetDefaultLogger()

			// Close the writer and read the output
			w.Close()
			var buf bytes.Buffer
			if _, err := io.Copy(&buf, r); err != nil {
				t.Fatal(err)
			}

			// Check the output
			output := buf.String()
			if !strings.Contains(output, tt.expected) {
				t.Errorf("expected output to contain %q, got %q", tt.expected, output)
			}
		})
	}
}
