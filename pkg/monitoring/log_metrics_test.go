// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package monitoring

import (
	"bytes"
	"log/slog"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"go.uber.org/zap/zapcore"
)

func TestTrimModulePrefix(t *testing.T) {
	tests := []struct {
		input, want string
	}{
		{
			input: "github.com/cobaltcore-dev/cortex/internal/scheduling/nova/external_scheduler_api.go",
			want:  "internal/scheduling/nova/external_scheduler_api.go",
		},
		{
			input: "/some/absolute/path/file.go",
			want:  "/some/absolute/path/file.go",
		},
		{
			input: "",
			want:  "",
		},
	}
	for _, tt := range tests {
		if got := trimModulePrefix(tt.input); got != tt.want {
			t.Errorf("trimModulePrefix(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

// newTestCounter creates a fresh CounterVec with the same schema as
// LogMessagesTotal, useful for isolated test assertions.
func newTestCounter() *prometheus.CounterVec {
	return prometheus.NewCounterVec(prometheus.CounterOpts{
		Namespace: "cortex",
		Name:      "log_messages_total",
		Help:      "Total number of log messages emitted at warn or error level.",
	}, []string{"level", "file"})
}

// gatherCounts collects the counter from a fresh registry and returns
// a map of level -> file -> count.
func gatherCounts(t *testing.T, counter *prometheus.CounterVec) map[string]map[string]float64 {
	t.Helper()
	reg := prometheus.NewRegistry()
	reg.MustRegister(counter)
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

func TestMetricsSlogHandler_Counts(t *testing.T) {
	tests := []struct {
		name        string
		emit        func(logger *slog.Logger)
		wantWarn    float64
		wantError   float64
		wantNoDebug bool
		wantNoInfo  bool
	}{
		{
			name: "one warn one error",
			emit: func(l *slog.Logger) {
				l.Warn("w")
				l.Error("e")
			},
			wantWarn:    1,
			wantError:   1,
			wantNoDebug: true,
			wantNoInfo:  true,
		},
		{
			name: "multiple warns",
			emit: func(l *slog.Logger) {
				l.Warn("w1")
				l.Warn("w2")
				l.Warn("w3")
			},
			wantWarn:    3,
			wantError:   0,
			wantNoDebug: true,
			wantNoInfo:  true,
		},
		{
			name: "multiple errors",
			emit: func(l *slog.Logger) {
				l.Error("e1")
				l.Error("e2")
			},
			wantWarn:    0,
			wantError:   2,
			wantNoDebug: true,
			wantNoInfo:  true,
		},
		{
			name: "debug and info are not counted",
			emit: func(l *slog.Logger) {
				l.Debug("d")
				l.Info("i")
			},
			wantWarn:    0,
			wantError:   0,
			wantNoDebug: true,
			wantNoInfo:  true,
		},
		{
			name: "mixed levels",
			emit: func(l *slog.Logger) {
				l.Debug("d")
				l.Info("i")
				l.Warn("w1")
				l.Warn("w2")
				l.Error("e1")
				l.Error("e2")
				l.Error("e3")
			},
			wantWarn:    2,
			wantError:   3,
			wantNoDebug: true,
			wantNoInfo:  true,
		},
		{
			name:        "no logs emitted",
			emit:        func(l *slog.Logger) {},
			wantWarn:    0,
			wantError:   0,
			wantNoDebug: true,
			wantNoInfo:  true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			orig := LogMessagesTotal
			LogMessagesTotal = newTestCounter()
			defer func() { LogMessagesTotal = orig }()

			var buf bytes.Buffer
			inner := slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug})
			logger := slog.New(NewMetricsSlogHandler(inner))

			tt.emit(logger)

			counts := gatherCounts(t, LogMessagesTotal)

			if got := sumLevel(counts, "warn"); got != tt.wantWarn {
				t.Errorf("warn count: got %v, want %v", got, tt.wantWarn)
			}
			if got := sumLevel(counts, "error"); got != tt.wantError {
				t.Errorf("error count: got %v, want %v", got, tt.wantError)
			}
			if tt.wantNoDebug && len(counts["debug"]) > 0 {
				t.Errorf("expected no debug counts, got %v", counts["debug"])
			}
			if tt.wantNoInfo && len(counts["info"]) > 0 {
				t.Errorf("expected no info counts, got %v", counts["info"])
			}
		})
	}
}

func TestMetricsSlogHandler_DelegatesAllLevels(t *testing.T) {
	orig := LogMessagesTotal
	LogMessagesTotal = newTestCounter()
	defer func() { LogMessagesTotal = orig }()

	var buf bytes.Buffer
	inner := slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug})
	logger := slog.New(NewMetricsSlogHandler(inner))

	logger.Debug("d")
	logger.Info("i")
	logger.Warn("w")
	logger.Error("e")

	output := buf.String()
	for _, msg := range []string{"d", "i", "w", "e"} {
		if !bytes.Contains([]byte(output), []byte(msg)) {
			t.Errorf("expected inner handler to receive message %q", msg)
		}
	}
}

func TestMetricsSlogHandler_WithAttrsAndGroup(t *testing.T) {
	orig := LogMessagesTotal
	LogMessagesTotal = newTestCounter()
	defer func() { LogMessagesTotal = orig }()

	var buf bytes.Buffer
	inner := slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug})
	handler := NewMetricsSlogHandler(inner)

	derived := handler.WithAttrs([]slog.Attr{slog.String("key", "val")})
	grouped := derived.WithGroup("grp")

	if _, ok := derived.(*MetricsSlogHandler); !ok {
		t.Fatalf("WithAttrs should return *MetricsSlogHandler, got %T", derived)
	}
	if _, ok := grouped.(*MetricsSlogHandler); !ok {
		t.Fatalf("WithGroup should return *MetricsSlogHandler, got %T", grouped)
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
		wantWarn    float64
		wantError   float64
		wantUnknown float64 // warn entries with no caller → file="unknown"
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
			name: "debug and info are not counted",
			entries: []entry{
				{zapcore.DebugLevel, true},
				{zapcore.InfoLevel, true},
			},
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
			orig := LogMessagesTotal
			LogMessagesTotal = newTestCounter()
			defer func() { LogMessagesTotal = orig }()

			sink := &zapcore.BufferedWriteSyncer{WS: zapcore.AddSync(&bytes.Buffer{})}
			inner := zapcore.NewCore(enc, sink, zapcore.DebugLevel)
			wrapped := WrapCoreWithLogMetrics(inner)

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

			counts := gatherCounts(t, LogMessagesTotal)

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
			if len(counts["debug"]) > 0 || len(counts["info"]) > 0 {
				t.Errorf("debug/info should not be counted, got debug=%v info=%v", counts["debug"], counts["info"])
			}
		})
	}
}
