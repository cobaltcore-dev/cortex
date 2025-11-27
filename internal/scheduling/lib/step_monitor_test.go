// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package lib

import (
	"log/slog"
	"os"
	"testing"
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
	removedSubjectsObserver := &mockObserver{}
	monitor := &StepMonitor[mockPipelineRequest]{
		stepName: "mock_step",
		Step: &mockStep[mockPipelineRequest]{
			RunFunc: func(traceLog *slog.Logger, request mockPipelineRequest) (*StepResult, error) {
				return &StepResult{
					Activations: map[string]float64{"subject1": 0.1, "subject2": 1.0, "subject3": 0.0},
				}, nil
			},
		},
		runTimer:                runTimer,
		stepSubjectWeight:       nil,
		removedSubjectsObserver: removedSubjectsObserver,
	}
	request := mockPipelineRequest{
		Subjects: []string{"subject1", "subject2", "subject3"},
		Weights:  map[string]float64{"subject1": 0.2, "subject2": 0.1, "subject3": 0.0},
	}
	if _, err := monitor.Run(slog.Default(), request); err != nil {
		t.Fatalf("Run() error = %v, want nil", err)
	}
	if len(removedSubjectsObserver.Observations) != 1 {
		t.Errorf("removedSubjectsObserver.Observations = %v, want 1", len(removedSubjectsObserver.Observations))
	}
	if removedSubjectsObserver.Observations[0] != 0 {
		t.Errorf("removedSubjectsObserver.Observations[0] = %v, want 0", removedSubjectsObserver.Observations[0])
	}
	if len(runTimer.Observations) != 1 {
		t.Errorf("runTimer.Observations = %v, want 1", len(runTimer.Observations))
	}
	if runTimer.Observations[0] <= 0 {
		t.Errorf("runTimer.Observations[0] = %v, want > 0", runTimer.Observations[0])
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
			name:     "Missing Subjects",
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
