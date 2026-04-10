// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package monitoring

import (
	"context"
	"log/slog"
	"path"
	"runtime"
	"sync"

	"github.com/prometheus/client_golang/prometheus"
	"go.uber.org/zap/zapcore"
)

// pcFileCache caches the resolved file path for each program counter. The set
// of distinct PCs is bounded by the number of log call sites in the binary, so
// this map grows to a fixed size and all subsequent lookups are lock-free reads.
var pcFileCache sync.Map // uintptr -> string

// LogMessagesTotal counts warn and error log messages emitted by both the slog
// and zap loggers. Labels: "level" (warn|error), "file" (relative source path).
var LogMessagesTotal = prometheus.NewCounterVec(prometheus.CounterOpts{
	Namespace: "cortex",
	Name:      "log_messages_total",
	Help:      "Total number of log messages emitted at warn or error level.",
}, []string{"level", "file"})

// shortFilePath returns "parent_dir/filename.go" from any absolute or
// module-relative path. This is independent of the build environment (no
// -trimpath needed) and keeps Prometheus label cardinality manageable.
func shortFilePath(file string) string {
	dir, base := path.Split(file)
	parent := path.Base(dir)
	if parent == "." || parent == "/" {
		return base
	}
	return parent + "/" + base
}

// --- slog handler wrapper ---

// MetricsSlogHandler wraps an slog.Handler and increments LogMessagesTotal for
// every warn or error log record.
type MetricsSlogHandler struct {
	next slog.Handler
}

// NewMetricsSlogHandler returns a new handler that counts warn/error logs and
// delegates all calls to next.
func NewMetricsSlogHandler(next slog.Handler) *MetricsSlogHandler {
	return &MetricsSlogHandler{next: next}
}

func (h *MetricsSlogHandler) Enabled(ctx context.Context, level slog.Level) bool {
	if h.next == nil {
		return false
	}
	return h.next.Enabled(ctx, level)
}

func (h *MetricsSlogHandler) Handle(ctx context.Context, r slog.Record) error {
	if r.Level >= slog.LevelWarn {
		level := "warn"
		if r.Level >= slog.LevelError {
			level = "error"
		}
		file := "unknown"
		if r.PC != 0 {
			if cached, ok := pcFileCache.Load(r.PC); ok {
				file = cached.(string)
			} else {
				frames := runtime.CallersFrames([]uintptr{r.PC})
				f, _ := frames.Next()
				if f.File != "" {
					file = shortFilePath(f.File)
				}
				pcFileCache.Store(r.PC, file)
			}
		}
		LogMessagesTotal.WithLabelValues(level, file).Inc()
	}
	if h.next == nil {
		return nil
	}
	return h.next.Handle(ctx, r)
}

func (h *MetricsSlogHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	if h.next == nil {
		return &MetricsSlogHandler{}
	}
	return &MetricsSlogHandler{next: h.next.WithAttrs(attrs)}
}

func (h *MetricsSlogHandler) WithGroup(name string) slog.Handler {
	if h.next == nil {
		return &MetricsSlogHandler{}
	}
	return &MetricsSlogHandler{next: h.next.WithGroup(name)}
}

// --- zap core wrapper ---

// WrapCoreWithLogMetrics returns a zapcore.Core that hooks into every write to
// increment LogMessagesTotal for warn and error entries. It uses
// zapcore.RegisterHooks so no manual Check/Write plumbing is needed.
func WrapCoreWithLogMetrics(core zapcore.Core) zapcore.Core {
	return zapcore.RegisterHooks(core, func(e zapcore.Entry) error {
		if e.Level >= zapcore.WarnLevel {
			level := "warn"
			if e.Level >= zapcore.ErrorLevel {
				level = "error"
			}
			file := "unknown"
			if e.Caller.Defined {
				file = shortFilePath(e.Caller.File)
			}
			LogMessagesTotal.WithLabelValues(level, file).Inc()
		}
		return nil
	})
}
