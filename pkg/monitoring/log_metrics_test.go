// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package monitoring

import (
	"bytes"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"go.uber.org/zap/zapcore"
)

func TestShortFilePath(t *testing.T) {
	tests := []struct {
		input, want string
	}{
		{
			input: "github.com/cobaltcore-dev/cortex/internal/scheduling/nova/external_scheduler_api.go",
			want:  "nova/external_scheduler_api.go",
		},
		{
			input: "/workspace/internal/knowledge/extractor/plugins/compute/vrops_hostsystem_contention_long_term.go",
			want:  "compute/vrops_hostsystem_contention_long_term.go",
		},
		{
			input: "/some/absolute/path/file.go",
			want:  "path/file.go",
		},
		{
			input: "file.go",
			want:  "file.go",
		},
		{
			input: "",
			want:  "",
		},
	}
	for _, tt := range tests {
		if got := shortFilePath(tt.input); got != tt.want {
			t.Errorf("shortFilePath(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

// gatherCounts collects the counter from a fresh registry and returns
// a map of level -> file -> count.
func gatherCounts(t *testing.T, monitor *LogMetricsMonitor) map[string]map[string]float64 {
	t.Helper()
	reg := prometheus.NewRegistry()
	reg.MustRegister(monitor)
	families, err := reg.Gather()
	if err != nil {
		t.Fatalf("gather failed: %v", err)
	}
	counts := make(map[string]map[string]float64)
	for _, fam := range families {
		if fam.GetName() != "cortex_log_messages_total" {
			continue
		}
		for _, m := range fam.GetMetric() {
			labels := make(map[string]string)
			for _, lp := range m.GetLabel() {
				labels[lp.GetName()] = lp.GetValue()
			}
			level := labels["level"]
			file := labels["file"]
			if counts[level] == nil {
				counts[level] = make(map[string]float64)
			}
			counts[level][file] += m.GetCounter().GetValue()
		}
	}
	return counts
}

// sumLevel sums all file counts for a given level label.
func sumLevel(counts map[string]map[string]float64, level string) float64 {
	total := 0.0
	for _, v := range counts[level] {
		total += v
	}
	return total
}

func TestLogMetricsMonitor_DescribeCollect(t *testing.T) {
	monitor := NewLogMetricsMonitor()
	// Verify Describe sends at least one descriptor.
	descCh := make(chan *prometheus.Desc, 10)
	monitor.Describe(descCh)
	close(descCh)
	if len(descCh) == 0 {
		t.Error("Describe should send at least one descriptor")
	}
	// Increment and verify Collect returns a metric.
	monitor.inc("warn", "test/file.go")
	metricCh := make(chan prometheus.Metric, 10)
	monitor.Collect(metricCh)
	close(metricCh)
	if len(metricCh) == 0 {
		t.Error("Collect should return at least one metric after incrementing")
	}
}

func TestWrapCoreWithLogMetrics(t *testing.T) {
	type entry struct {
		level  zapcore.Level
		caller bool
	}
	tests := []struct {
		name        string
		entries     []entry
		wantInfo    float64
		wantWarn    float64
		wantError   float64
		wantUnknown float64
	}{
		{
			name: "one warn one error with caller",
			entries: []entry{
				{zapcore.WarnLevel, true},
				{zapcore.ErrorLevel, true},
			},
			wantWarn:  1,
			wantError: 1,
		},
		{
			name: "multiple warns",
			entries: []entry{
				{zapcore.WarnLevel, true},
				{zapcore.WarnLevel, true},
				{zapcore.WarnLevel, true},
			},
			wantWarn:  3,
			wantError: 0,
		},
		{
			name: "multiple errors",
			entries: []entry{
				{zapcore.ErrorLevel, true},
				{zapcore.ErrorLevel, true},
			},
			wantWarn:  0,
			wantError: 2,
		},
		{
			name: "info is counted, debug is not",
			entries: []entry{
				{zapcore.DebugLevel, true},
				{zapcore.InfoLevel, true},
			},
			wantInfo:  1,
			wantWarn:  0,
			wantError: 0,
		},
		{
			name: "warn without caller uses unknown file",
			entries: []entry{
				{zapcore.WarnLevel, false},
				{zapcore.WarnLevel, false},
			},
			wantWarn:    2,
			wantError:   0,
			wantUnknown: 2,
		},
		{
			name: "mixed levels and callers",
			entries: []entry{
				{zapcore.DebugLevel, true},
				{zapcore.InfoLevel, true},
				{zapcore.WarnLevel, true},
				{zapcore.WarnLevel, false},
				{zapcore.ErrorLevel, true},
				{zapcore.ErrorLevel, true},
			},
			wantInfo:    1,
			wantWarn:    2,
			wantError:   2,
			wantUnknown: 1,
		},
		{
			name:      "dpanic and above count as error",
			entries:   []entry{{zapcore.DPanicLevel, true}, {zapcore.PanicLevel, true}},
			wantWarn:  0,
			wantError: 2,
		},
	}

	enc := zapcore.NewJSONEncoder(zapcore.EncoderConfig{
		MessageKey:  "msg",
		LevelKey:    "level",
		TimeKey:     "ts",
		EncodeTime:  zapcore.ISO8601TimeEncoder,
		EncodeLevel: zapcore.LowercaseLevelEncoder,
	})

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			monitor := NewLogMetricsMonitor()
			sink := zapcore.AddSync(&bytes.Buffer{})
			inner := zapcore.NewCore(enc, sink, zapcore.DebugLevel)
			wrapped := WrapCoreWithLogMetrics(&monitor, inner)
			for i, e := range tt.entries {
				ent := zapcore.Entry{Level: e.level, Message: "msg", Time: time.Now()}
				if e.caller {
					ent.Caller = zapcore.EntryCaller{
						Defined: true,
						File:    "github.com/cobaltcore-dev/cortex/internal/test/fake.go",
						Line:    i,
					}
				}
				if ce := wrapped.Check(ent, nil); ce != nil {
					ce.Write()
				}
			}
			counts := gatherCounts(t, &monitor)
			if got := sumLevel(counts, "info"); got != tt.wantInfo {
				t.Errorf("info count: got %v, want %v", got, tt.wantInfo)
			}
			if got := sumLevel(counts, "warn"); got != tt.wantWarn {
				t.Errorf("warn count: got %v, want %v", got, tt.wantWarn)
			}
			if got := sumLevel(counts, "error"); got != tt.wantError {
				t.Errorf("error count: got %v, want %v", got, tt.wantError)
			}
			if tt.wantUnknown > 0 {
				if got := counts["warn"]["unknown"]; got != tt.wantUnknown {
					t.Errorf("warn[unknown] count: got %v, want %v", got, tt.wantUnknown)
				}
			}
			if len(counts["debug"]) > 0 {
				t.Errorf("debug should not be counted, got debug=%v", counts["debug"])
			}
		})
	}
}
