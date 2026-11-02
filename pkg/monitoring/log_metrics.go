// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package monitoring

import (
	"path"

	"github.com/prometheus/client_golang/prometheus"
	"go.uber.org/zap/zapcore"
)

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
			Help:      "Total number of log messages emitted at info, warn or error level.",
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

// --- zap core wrapper ---

// WrapCoreWithLogMetrics returns a zapcore.Core that hooks into every write to
// increment the log-message counter for warn and error entries.
func WrapCoreWithLogMetrics(monitor *LogMetricsMonitor, core zapcore.Core) zapcore.Core {
	return zapcore.RegisterHooks(core, func(e zapcore.Entry) error {
		if e.Level >= zapcore.InfoLevel {
			level := "info"
			switch {
			case e.Level >= zapcore.ErrorLevel:
				level = "error"
			case e.Level >= zapcore.WarnLevel:
				level = "warn"
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
