// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package reservations

import (
	"bytes"
	"errors"
	"log/slog"
	"strings"
	"testing"

	"github.com/go-logr/logr"
)

func TestSlogLogSink_Init(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&buf, nil))
	sink := SlogLogSink{log: logger}

	// Init should not panic and should be a no-op
	sink.Init(logr.RuntimeInfo{})

	// Buffer should be empty since Init does nothing
	if buf.Len() != 0 {
		t.Errorf("Expected Init to be a no-op, but got output: %s", buf.String())
	}
}

func TestSlogLogSink_Enabled(t *testing.T) {
	tests := []struct {
		name     string
		level    slog.Level
		logLevel int
		expected bool
	}{
		{
			name:     "debug level enabled",
			level:    slog.LevelDebug,
			logLevel: -4, // logr debug level
			expected: true,
		},
		{
			name:     "info level enabled",
			level:    slog.LevelInfo,
			logLevel: 0, // logr info level
			expected: true,
		},
		{
			name:     "warn level disabled when level is error",
			level:    slog.LevelError,
			logLevel: 0, // logr info level (warn maps to info)
			expected: false,
		},
		{
			name:     "error level enabled",
			level:    slog.LevelError,
			logLevel: int(slog.LevelError), // Use slog error level directly
			expected: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var buf bytes.Buffer
			opts := &slog.HandlerOptions{Level: tt.level}
			logger := slog.New(slog.NewTextHandler(&buf, opts))
			sink := SlogLogSink{log: logger}

			result := sink.Enabled(tt.logLevel)
			if result != tt.expected {
				t.Errorf("Expected Enabled(%d) to return %v, got %v", tt.logLevel, tt.expected, result)
			}
		})
	}
}

func TestSlogLogSink_Info(t *testing.T) {
	tests := []struct {
		name          string
		level         int
		msg           string
		keysAndValues []any
		expectedInLog []string
		notInLog      []string
	}{
		{
			name:          "simple info message",
			level:         0,
			msg:           "test message",
			keysAndValues: nil,
			expectedInLog: []string{"test message"},
		},
		{
			name:          "info with key-value pairs",
			level:         0,
			msg:           "operation completed",
			keysAndValues: []any{"user", "john", "duration", "5s"},
			expectedInLog: []string{"operation completed", "user=john", "duration=5s"},
		},
		{
			name:          "info with mixed types",
			level:         0,
			msg:           "processing request",
			keysAndValues: []any{"id", 123, "success", true},
			expectedInLog: []string{"processing request", "id=123", "success=true"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var buf bytes.Buffer
			logger := slog.New(slog.NewTextHandler(&buf, nil))
			sink := SlogLogSink{log: logger}

			sink.Info(tt.level, tt.msg, tt.keysAndValues...)

			output := buf.String()
			for _, expected := range tt.expectedInLog {
				if !strings.Contains(output, expected) {
					t.Errorf("Expected log output to contain %q, but got: %s", expected, output)
				}
			}
			for _, notExpected := range tt.notInLog {
				if strings.Contains(output, notExpected) {
					t.Errorf("Expected log output to NOT contain %q, but got: %s", notExpected, output)
				}
			}
		})
	}
}

func TestSlogLogSink_Error(t *testing.T) {
	tests := []struct {
		name          string
		err           error
		msg           string
		keysAndValues []any
		expectedInLog []string
	}{
		{
			name:          "simple error",
			err:           errors.New("test error"),
			msg:           "operation failed",
			keysAndValues: nil,
			expectedInLog: []string{"operation failed", "error=\"test error\""},
		},
		{
			name:          "error with context",
			err:           errors.New("connection timeout"),
			msg:           "database operation failed",
			keysAndValues: []any{"table", "users", "retry", 3},
			expectedInLog: []string{"database operation failed", "table=users", "retry=3", "error=\"connection timeout\""},
		},
		{
			name:          "nil error",
			err:           nil,
			msg:           "warning message",
			keysAndValues: []any{"component", "scheduler"},
			expectedInLog: []string{"warning message", "component=scheduler", "error=<nil>"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var buf bytes.Buffer
			logger := slog.New(slog.NewTextHandler(&buf, nil))
			sink := SlogLogSink{log: logger}

			sink.Error(tt.err, tt.msg, tt.keysAndValues...)

			output := buf.String()
			for _, expected := range tt.expectedInLog {
				if !strings.Contains(output, expected) {
					t.Errorf("Expected log output to contain %q, but got: %s", expected, output)
				}
			}
		})
	}
}

func TestSlogLogSink_WithValues(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&buf, nil))
	sink := SlogLogSink{log: logger}

	// Create a new sink with additional values
	newSink := sink.WithValues("component", "reservations", "version", "1.0")

	// Verify it returns a new SlogLogSink
	if _, ok := newSink.(SlogLogSink); !ok {
		t.Errorf("Expected WithValues to return SlogLogSink, got %T", newSink)
	}

	// Test that the new sink includes the additional values
	newSink.Info(0, "test message")

	output := buf.String()
	expectedValues := []string{"component=reservations", "version=1.0", "test message"}
	for _, expected := range expectedValues {
		if !strings.Contains(output, expected) {
			t.Errorf("Expected log output to contain %q, but got: %s", expected, output)
		}
	}
}

func TestSlogLogSink_WithName(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&buf, nil))
	sink := SlogLogSink{log: logger}

	// Create a new sink with a name
	newSink := sink.WithName("test-component")

	// Verify it returns a new SlogLogSink
	if _, ok := newSink.(SlogLogSink); !ok {
		t.Errorf("Expected WithName to return SlogLogSink, got %T", newSink)
	}

	// Test that the new sink includes the name
	newSink.Info(0, "test message")

	output := buf.String()
	expectedValues := []string{"name=test-component", "test message"}
	for _, expected := range expectedValues {
		if !strings.Contains(output, expected) {
			t.Errorf("Expected log output to contain %q, but got: %s", expected, output)
		}
	}
}

func TestSlogLogSink_ChainedOperations(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&buf, nil))
	sink := SlogLogSink{log: logger}

	// Chain WithName and WithValues operations
	chainedSink := sink.WithName("operator").WithValues("namespace", "default", "resource", "reservation")

	// Test that all values are preserved
	chainedSink.Info(0, "reconciling resource")

	output := buf.String()
	expectedValues := []string{
		"name=operator",
		"namespace=default",
		"resource=reservation",
		"reconciling resource",
	}
	for _, expected := range expectedValues {
		if !strings.Contains(output, expected) {
			t.Errorf("Expected log output to contain %q, but got: %s", expected, output)
		}
	}
}

func TestSlogLogSink_IntegrationWithLogr(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&buf, nil))
	sink := SlogLogSink{log: logger}

	// Create a logr.Logger using our sink
	logrLogger := logr.New(sink)

	// Test various logr operations
	logrLogger.Info("starting operation", "id", "123")
	logrLogger.Error(errors.New("test error"), "operation failed", "retry", true)

	namedLogger := logrLogger.WithName("controller")
	namedLogger.Info("controller started")

	valueLogger := logrLogger.WithValues("component", "scheduler")
	valueLogger.Info("scheduling complete")

	output := buf.String()
	expectedMessages := []string{
		"starting operation",
		"id=123",
		"operation failed",
		"error=\"test error\"",
		"retry=true",
		"name=controller",
		"controller started",
		"component=scheduler",
		"scheduling complete",
	}

	for _, expected := range expectedMessages {
		if !strings.Contains(output, expected) {
			t.Errorf("Expected log output to contain %q, but got: %s", expected, output)
		}
	}
}

// Benchmark tests to ensure the logging implementation is performant
func BenchmarkSlogLogSink_Info(b *testing.B) {
	var buf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&buf, nil))
	sink := SlogLogSink{log: logger}

	b.ResetTimer()
	for i := range b.N {
		sink.Info(0, "benchmark message", "iteration", i, "component", "test")
	}
}

func BenchmarkSlogLogSink_Error(b *testing.B) {
	var buf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&buf, nil))
	sink := SlogLogSink{log: logger}
	err := errors.New("benchmark error")

	b.ResetTimer()
	for i := range b.N {
		sink.Error(err, "benchmark error message", "iteration", i)
	}
}

func BenchmarkSlogLogSink_WithValues(b *testing.B) {
	var buf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&buf, nil))
	sink := SlogLogSink{log: logger}

	b.ResetTimer()
	for i := range b.N {
		newSink := sink.WithValues("iteration", i, "component", "benchmark")
		newSink.Info(0, "test message")
	}
}
