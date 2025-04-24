// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package scheduler

import (
	"testing"

	"github.com/cobaltcore-dev/cortex/internal/scheduler/api"
	"github.com/cobaltcore-dev/cortex/testlib/monitoring"
	testlibAPI "github.com/cobaltcore-dev/cortex/testlib/scheduler/api"
	"github.com/cobaltcore-dev/cortex/testlib/scheduler/plugins"
)

func TestStepMonitorRun(t *testing.T) {
	runTimer := &monitoring.MockObserver{}
	removedHostsObserver := &monitoring.MockObserver{}
	reorderingsObserver := &monitoring.MockObserver{}
	monitor := &StepMonitor{
		Step: &plugins.MockStep{
			Name: "mock_step",
			RunFunc: func(request api.Request) (map[string]float64, error) {
				return map[string]float64{"host1": 0.0, "host2": 1.0, "host3": 0.0}, nil
			},
		},
		runTimer:             runTimer,
		stepHostWeight:       nil,
		removedHostsObserver: removedHostsObserver,
		reorderingsObserver:  reorderingsObserver,
	}
	request := &testlibAPI.MockRequest{
		Hosts:   []string{"host1", "host2", "host3"},
		Weights: map[string]float64{"host1": 0.0, "host2": 0.0, "host3": 0.0},
	}
	if _, err := monitor.Run(request); err != nil {
		t.Fatalf("Run() error = %v, want nil", err)
	}
	if len(removedHostsObserver.Observations) != 1 {
		t.Errorf("removedHostsObserver.Observations = %v, want 1", len(removedHostsObserver.Observations))
	}
	if removedHostsObserver.Observations[0] != 0 {
		t.Errorf("removedHostsObserver.Observations[0] = %v, want 0", removedHostsObserver.Observations[0])
	}
	if len(reorderingsObserver.Observations) != 1 {
		t.Errorf("reorderingsObserver.Observations = %v, want 1", len(reorderingsObserver.Observations))
	}
	if reorderingsObserver.Observations[0] != 2 {
		t.Errorf("reorderingsObserver.Observations[0] = %v, want 2", reorderingsObserver.Observations[0])
	}
	if len(runTimer.Observations) != 1 {
		t.Errorf("runTimer.Observations = %v, want 1", len(runTimer.Observations))
	}
	if runTimer.Observations[0] <= 0 {
		t.Errorf("runTimer.Observations[0] = %v, want > 0", runTimer.Observations[0])
	}
}

func TestLevenshteinDistance(t *testing.T) {
	tests := []struct {
		name     string
		a        []string
		b        []string
		expected int
	}{
		{
			name:     "Identical slices",
			a:        []string{"host1", "host2", "host3"},
			b:        []string{"host1", "host2", "host3"},
			expected: 0,
		},
		{
			name:     "Completely different slices",
			a:        []string{"host1", "host2", "host3"},
			b:        []string{"host4", "host5", "host6"},
			expected: 3,
		},
		{
			name:     "One insertion",
			a:        []string{"host1", "host2"},
			b:        []string{"host1", "host2", "host3"},
			expected: 1,
		},
		{
			name:     "One deletion",
			a:        []string{"host1", "host2", "host3"},
			b:        []string{"host1", "host2"},
			expected: 1,
		},
		{
			name:     "One substitution",
			a:        []string{"host1", "host2", "host3"},
			b:        []string{"host1", "hostX", "host3"},
			expected: 1,
		},
		{
			name:     "Empty slices",
			a:        []string{},
			b:        []string{},
			expected: 0,
		},
		{
			name:     "One empty slice",
			a:        []string{"host1", "host2", "host3"},
			b:        []string{},
			expected: 3,
		},
		{
			name:     "Reordering",
			a:        []string{"host1", "host2", "host3"},
			b:        []string{"host3", "host2", "host1"},
			expected: 2,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := levenshteinDistance(tt.a, tt.b)
			if result != tt.expected {
				t.Errorf("levenshteinDistance(%v, %v) = %d; want %d", tt.a, tt.b, result, tt.expected)
			}
		})
	}
}
