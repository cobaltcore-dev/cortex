// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package lib

import (
	"log/slog"
	"os"
	"strings"
	"testing"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"
)

type mockObserver struct {
	// Observations recorded by the mock observer.
	Observations []float64
}

func (m *mockObserver) Observe(value float64) {
	m.Observations = append(m.Observations, value)
}

func TestStepMonitorRun(t *testing.T) {
	runTimer := &mockObserver{}
	removedHostsObserver := &mockObserver{}
	monitor := &FilterWeigherPipelineStepMonitor[mockFilterWeigherPipelineRequest]{
		stepName:             "mock_step",
		runTimer:             runTimer,
		stepHostWeight:       nil,
		removedHostsObserver: removedHostsObserver,
	}
	step := &mockWeigher[mockFilterWeigherPipelineRequest]{
		RunFunc: func(traceLog *slog.Logger, request mockFilterWeigherPipelineRequest) (*FilterWeigherPipelineStepResult, error) {
			return &FilterWeigherPipelineStepResult{
				Activations: map[string]float64{"host1": 0.1, "host2": 1.0, "host3": 0.0},
			}, nil
		},
	}
	request := mockFilterWeigherPipelineRequest{
		Hosts:   []string{"host1", "host2", "host3"},
		Weights: map[string]float64{"host1": 0.2, "host2": 0.1, "host3": 0.0},
	}
	if _, err := monitor.RunWrapped(slog.Default(), request, step); err != nil {
		t.Fatalf("Run() error = %v, want nil", err)
	}
	if len(removedHostsObserver.Observations) != 1 {
		t.Errorf("removedHostsObserver.Observations = %v, want 1", len(removedHostsObserver.Observations))
	}
	if removedHostsObserver.Observations[0] != 0 {
		t.Errorf("removedHostsObserver.Observations[0] = %v, want 0", removedHostsObserver.Observations[0])
	}
	if len(runTimer.Observations) != 1 {
		t.Errorf("runTimer.Observations = %v, want 1", len(runTimer.Observations))
	}
	if runTimer.Observations[0] <= 0 {
		t.Errorf("runTimer.Observations[0] = %v, want > 0", runTimer.Observations[0])
	}
}

func TestStepMonitorRunEvents(t *testing.T) {
	stepEventCollector := newPipelineStepEventCollector()
	monitor := &FilterWeigherPipelineStepMonitor[mockFilterWeigherPipelineRequest]{
		stepName:           "mock_step",
		pipelineName:       "mock_pipeline",
		stepEventCollector: stepEventCollector,
	}
	step := &mockWeigher[mockFilterWeigherPipelineRequest]{
		RunFunc: func(traceLog *slog.Logger, request mockFilterWeigherPipelineRequest) (*FilterWeigherPipelineStepResult, error) {
			return &FilterWeigherPipelineStepResult{
				Activations: map[string]float64{"host1": 0.0},
				Events: []FilterWeigherPipelineStepEvent{
					{Name: "hypervisor_type_undetermined"},
				},
			}, nil
		},
	}
	request := mockFilterWeigherPipelineRequest{
		Hosts:   []string{"host1"},
		Weights: map[string]float64{"host1": 0.0},
	}
	if _, err := monitor.RunWrapped(slog.Default(), request, step); err != nil {
		t.Fatalf("Run() error = %v, want nil", err)
	}
	if got := len(stepEventCollector.counts); got != 1 {
		t.Fatalf("stepEventCollector.counts = %v, want 1", got)
	}
	for _, count := range stepEventCollector.counts {
		if count != 1 {
			t.Errorf("stepEventCollector count = %v, want 1", count)
		}
	}
}

func TestStepMonitorRunEventsWithDynamicLabels(t *testing.T) {
	stepEventCollector := newPipelineStepEventCollector()
	monitor := &FilterWeigherPipelineStepMonitor[mockFilterWeigherPipelineRequest]{
		stepName:           "mock_step",
		pipelineName:       "mock_pipeline",
		stepEventCollector: stepEventCollector,
	}
	step := &mockWeigher[mockFilterWeigherPipelineRequest]{
		RunFunc: func(traceLog *slog.Logger, request mockFilterWeigherPipelineRequest) (*FilterWeigherPipelineStepResult, error) {
			return &FilterWeigherPipelineStepResult{
				Activations: map[string]float64{"host1": 0.0},
				Events: []FilterWeigherPipelineStepEvent{
					{
						Name:   "hypervisor_type_undetermined",
						Labels: map[string]string{"intent": "create"},
					},
				},
			}, nil
		},
	}
	request := mockFilterWeigherPipelineRequest{
		Hosts:   []string{"host1"},
		Weights: map[string]float64{"host1": 0.0},
	}
	if _, err := monitor.RunWrapped(slog.Default(), request, step); err != nil {
		t.Fatalf("Run() error = %v, want nil", err)
	}
	registry := prometheus.NewRegistry()
	registry.MustRegister(stepEventCollector)
	expected := `
		# HELP cortex_filter_weigher_pipeline_step_events_total Number of named events reported by a scheduler pipeline step
		# TYPE cortex_filter_weigher_pipeline_step_events_total counter
		cortex_filter_weigher_pipeline_step_events_total{event="hypervisor_type_undetermined",intent="create",pipeline="mock_pipeline",step="mock_step"} 1
	`
	if err := testutil.GatherAndCompare(registry, strings.NewReader(expected)); err != nil {
		t.Fatalf("GatherAndCompare() error = %v", err)
	}
}

func TestImpact(t *testing.T) {
	testcases := []struct {
		name     string
		before   []string
		after    []string
		stats    map[string]float64
		expected float64
	}{
		{
			name:   "Flip around",
			before: []string{"h0", "h1", "h2", "h3"},
			after:  []string{"h3", "h2", "h1", "h0"},
			// Let's say, these are cpu contention stats
			stats: map[string]float64{"h0": 30.0, "h1": 20.0, "h2": 10.0, "h3": 0.0},
			// h0 -> h3: abs(30.0 - 0.0)  * abs(0 - 3) = 90.0
			// h1 -> h2: abs(20.0 - 10.0) * abs(1 - 2) = 10.0
			// h2 -> h1: abs(10.0 - 20.0) * abs(2 - 1) = 10.0
			// h3 -> h0: abs(0.0 - 30.0)  * abs(3 - 0) = 90.0
			// Total impact % cpu contention shuffled = 200.0
			expected: 200.0,
		},
		{
			name:     "No Change",
			before:   []string{"h0", "h1", "h2", "h3"},
			after:    []string{"h0", "h1", "h2", "h3"},
			stats:    map[string]float64{"h0": 30.0, "h1": 20.0, "h2": 10.0, "h3": 0.0},
			expected: 0.0,
		},
		{
			name:   "Partial Reordering",
			before: []string{"h0", "h1", "h2", "h3"},
			after:  []string{"h0", "h2", "h1", "h3"},
			stats:  map[string]float64{"h0": 30.0, "h1": 20.0, "h2": 10.0, "h3": 0.0},
			// h0 -> h0: abs(30.0 - 30.0) * abs(0 - 0) = 0.0
			// h1 -> h2: abs(20.0 - 10.0) * abs(1 - 2) = 10.0
			// h2 -> h1: abs(10.0 - 20.0) * abs(2 - 1) = 10.0
			// h3 -> h3: abs(0.0 - 0.0) * abs(3 - 3) = 0.0
			// Total impact	= 20.0
			expected: 20.0,
		},
		{
			name:   "From far back to front",
			before: []string{"h0", "h1", "h2", "h3"},
			after:  []string{"h3", "h0", "h1", "h2"},
			stats:  map[string]float64{"h0": 30.0, "h1": 20.0, "h2": 10.0, "h3": 0.0},
			// h0 -> h3: abs(30.0 - 0.0) * abs(0 - 3) = 90.0
			// h1 -> h0: abs(20.0 - 30.0) * abs(1 - 0) = 10.0
			// h2 -> h1: abs(10.0 - 20.0) * abs(2 - 1) = 10.0
			// h3 -> h2: abs(0.0 - 10.0) * abs(3 - 2) = 10.0
			// Total impact = 120.0
			expected: 120.0,
		},
		{
			name:   "Top K > 5",
			before: []string{"h0", "h1", "h2", "h3", "h4", "h5", "h6"},
			after:  []string{"h0", "h1", "h2", "h3", "h4", "h6", "h5"},
			stats:  map[string]float64{"h0": 30.0, "h1": 20.0, "h2": 10.0, "h3": 0.0, "h4": 5.0, "h5": 2.0, "h6": 1.0},
			// h5 -> h6 should be ignored
			expected: 0.0,
		},
		{
			name:     "Missing Hosts",
			before:   []string{"h0", "h1", "h2", "h3"},
			after:    []string{"h0", "h1"},
			stats:    map[string]float64{"h0": 30.0, "h1": 20.0, "h2": 10.0, "h3": 0.0},
			expected: 0.0,
		},
		{
			name:     "Empty States",
			before:   []string{},
			after:    []string{},
			stats:    map[string]float64{},
			expected: 0.0,
		},
	}

	opts := &slog.HandlerOptions{Level: slog.LevelDebug}
	handler := slog.NewTextHandler(os.Stdout, opts)
	slog.SetDefault(slog.New(handler))
	for _, tc := range testcases {
		t.Run(tc.name, func(t *testing.T) {
			impactValue, err := impact(tc.before, tc.after, tc.stats, 5)
			if err != nil {
				t.Fatalf("impact() error = %v", err)
			}
			if impactValue != tc.expected {
				t.Errorf("impact() = %v, want %v", impactValue, tc.expected)
			}
		})
	}
}

func TestPipelineStepEventCollector_DynamicLabels(t *testing.T) {
	collector := newPipelineStepEventCollector()
	collector.Record("pipeline", "step", "event_a", map[string]string{"intent": "create"})
	collector.Record("pipeline", "step", "event_a", map[string]string{"intent": "create"})
	collector.Record("pipeline", "step", "event_a", map[string]string{"intent": "resize"})
	collector.Record("pipeline", "step", "event_b", map[string]string{"foo": "bar"})

	registry := prometheus.NewRegistry()
	registry.MustRegister(collector)

	expected := `
		# HELP cortex_filter_weigher_pipeline_step_events_total Number of named events reported by a scheduler pipeline step
		# TYPE cortex_filter_weigher_pipeline_step_events_total counter
		cortex_filter_weigher_pipeline_step_events_total{event="event_a",intent="create",pipeline="pipeline",step="step"} 2
		cortex_filter_weigher_pipeline_step_events_total{event="event_a",intent="resize",pipeline="pipeline",step="step"} 1
		cortex_filter_weigher_pipeline_step_events_total{event="event_b",foo="bar",pipeline="pipeline",step="step"} 1
	`
	if err := testutil.GatherAndCompare(registry, strings.NewReader(expected)); err != nil {
		t.Fatalf("GatherAndCompare() error = %v", err)
	}
}

func TestPipelineStepEventCollector_NoLabels(t *testing.T) {
	collector := newPipelineStepEventCollector()
	collector.Record("pipeline", "step", "event_c", nil)

	registry := prometheus.NewRegistry()
	registry.MustRegister(collector)

	expected := `
		# HELP cortex_filter_weigher_pipeline_step_events_total Number of named events reported by a scheduler pipeline step
		# TYPE cortex_filter_weigher_pipeline_step_events_total counter
		cortex_filter_weigher_pipeline_step_events_total{event="event_c",pipeline="pipeline",step="step"} 1
	`
	if err := testutil.GatherAndCompare(registry, strings.NewReader(expected)); err != nil {
		t.Fatalf("GatherAndCompare() error = %v", err)
	}
}

func TestPipelineStepEventCollector_DescribeIsNoOp(t *testing.T) {
	collector := newPipelineStepEventCollector()
	ch := make(chan *prometheus.Desc, 1)
	collector.Describe(ch)
	close(ch)
	if len(ch) != 0 {
		t.Errorf("expected Describe to send no descriptors, got %d", len(ch))
	}
}
