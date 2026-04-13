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

// LogMetricsMonitor owns the log-message counter and implements the
// prometheus.Collector interface so it can be registered with the metrics
// registry like every other Monitor in cortex.
type LogMetricsMonitor struct {
	counter *prometheus.CounterVec
}

// NewLogMetricsMonitor creates a new monitor for log-level metrics.
func NewLogMetricsMonitor() LogMetricsMonitor {
	return LogMetricsMonitor{
		counter: prometheus.NewCounterVec(prometheus.CounterOpts{
			Namespace: "cortex",
			Name:      "log_messages_total",
			Help:      "Total number of log messages emitted at warn or error level.",
		}, []string{"level", "file"}),
	}
}

func (m *LogMetricsMonitor) Describe(ch chan<- *prometheus.Desc) {
	m.counter.Describe(ch)
}

func (m *LogMetricsMonitor) Collect(ch chan<- prometheus.Metric) {
	m.counter.Collect(ch)
}

func (m *LogMetricsMonitor) inc(level, file string) {
	m.counter.WithLabelValues(level, file).Inc()
}

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

// MetricsSlogHandler wraps an slog.Handler and increments the log-message
// counter for every warn or error log record.
type MetricsSlogHandler struct {
	monitor *LogMetricsMonitor
	next    slog.Handler
}

// NewMetricsSlogHandler returns a new handler that counts warn/error logs and
// delegates all calls to next.
func NewMetricsSlogHandler(monitor *LogMetricsMonitor, next slog.Handler) *MetricsSlogHandler {
	return &MetricsSlogHandler{monitor: monitor, next: next}
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
		h.monitor.inc(level, file)
	}
	if h.next == nil {
		return nil
	}
	return h.next.Handle(ctx, r)
}

func (h *MetricsSlogHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	if h.next == nil {
		return &MetricsSlogHandler{monitor: h.monitor}
	}
	return &MetricsSlogHandler{monitor: h.monitor, next: h.next.WithAttrs(attrs)}
}

func (h *MetricsSlogHandler) WithGroup(name string) slog.Handler {
	if h.next == nil {
		return &MetricsSlogHandler{monitor: h.monitor}
	}
	return &MetricsSlogHandler{monitor: h.monitor, next: h.next.WithGroup(name)}
}

// --- zap core wrapper ---

// WrapCoreWithLogMetrics returns a zapcore.Core that hooks into every write to
// increment the log-message counter for warn and error entries.
func WrapCoreWithLogMetrics(monitor *LogMetricsMonitor, core zapcore.Core) zapcore.Core {
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
			monitor.inc(level, file)
		}
		return nil
	})
}
