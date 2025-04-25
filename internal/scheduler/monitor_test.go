// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package scheduler

import (
	"strings"
	"testing"

	"github.com/cobaltcore-dev/cortex/internal/monitoring"
	"github.com/cobaltcore-dev/cortex/internal/scheduler/api"
	testlibMonitoring "github.com/cobaltcore-dev/cortex/testlib/monitoring"
	testlibAPI "github.com/cobaltcore-dev/cortex/testlib/scheduler/api"
	"github.com/cobaltcore-dev/cortex/testlib/scheduler/plugins"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"
)

func TestSchedulerMonitor(t *testing.T) {
	registry := &monitoring.Registry{Registry: prometheus.NewRegistry()}
	monitor := NewSchedulerMonitor(registry)

	// Test stepRunTimer
	expectedStepRunTimer := strings.NewReader(`
        # HELP cortex_scheduler_pipeline_step_run_duration_seconds Duration of scheduler pipeline step run
        # TYPE cortex_scheduler_pipeline_step_run_duration_seconds histogram
        cortex_scheduler_pipeline_step_run_duration_seconds_bucket{step="test_step",le="0.005"} 1
        cortex_scheduler_pipeline_step_run_duration_seconds_bucket{step="test_step",le="0.01"} 1
        cortex_scheduler_pipeline_step_run_duration_seconds_bucket{step="test_step",le="0.025"} 1
        cortex_scheduler_pipeline_step_run_duration_seconds_bucket{step="test_step",le="0.05"} 1
        cortex_scheduler_pipeline_step_run_duration_seconds_bucket{step="test_step",le="0.1"} 1
        cortex_scheduler_pipeline_step_run_duration_seconds_bucket{step="test_step",le="0.25"} 1
        cortex_scheduler_pipeline_step_run_duration_seconds_bucket{step="test_step",le="0.5"} 1
        cortex_scheduler_pipeline_step_run_duration_seconds_bucket{step="test_step",le="1"} 1
        cortex_scheduler_pipeline_step_run_duration_seconds_bucket{step="test_step",le="2.5"} 1
        cortex_scheduler_pipeline_step_run_duration_seconds_bucket{step="test_step",le="5"} 1
        cortex_scheduler_pipeline_step_run_duration_seconds_bucket{step="test_step",le="10"} 1
        cortex_scheduler_pipeline_step_run_duration_seconds_bucket{step="test_step",le="+Inf"} 1
        cortex_scheduler_pipeline_step_run_duration_seconds_sum{step="test_step"} 0
        cortex_scheduler_pipeline_step_run_duration_seconds_count{step="test_step"} 1
    `)
	monitor.stepRunTimer.WithLabelValues("test_step").Observe(0)
	err := testutil.GatherAndCompare(registry, expectedStepRunTimer, "cortex_scheduler_pipeline_step_run_duration_seconds")
	if err != nil {
		t.Fatalf("stepRunTimer test failed: %v", err)
	}

	// Test stepHostWeight
	expectedStepHostWeight := strings.NewReader(`
        # HELP cortex_scheduler_pipeline_step_weight_modification Modification of host weight by scheduler pipeline step
        # TYPE cortex_scheduler_pipeline_step_weight_modification gauge
        cortex_scheduler_pipeline_step_weight_modification{host="test_host",step="test_step"} 42
    `)
	monitor.stepHostWeight.WithLabelValues("test_host", "test_step").Set(42)
	err = testutil.GatherAndCompare(registry, expectedStepHostWeight, "cortex_scheduler_pipeline_step_weight_modification")
	if err != nil {
		t.Fatalf("stepHostWeight test failed: %v", err)
	}

	// Test stepRemovedHostsObserver
	expectedRemovedHostsObserver := strings.NewReader(`
        # HELP cortex_scheduler_pipeline_step_removed_hosts Number of hosts removed by scheduler pipeline step
        # TYPE cortex_scheduler_pipeline_step_removed_hosts histogram
        cortex_scheduler_pipeline_step_removed_hosts_bucket{step="test_step",le="1"} 1
        cortex_scheduler_pipeline_step_removed_hosts_bucket{step="test_step",le="2.154434690031884"} 1
        cortex_scheduler_pipeline_step_removed_hosts_bucket{step="test_step",le="4.641588833612779"} 1
        cortex_scheduler_pipeline_step_removed_hosts_bucket{step="test_step",le="10.000000000000002"} 1
        cortex_scheduler_pipeline_step_removed_hosts_bucket{step="test_step",le="21.544346900318843"} 1
        cortex_scheduler_pipeline_step_removed_hosts_bucket{step="test_step",le="46.4158883361278"} 1
        cortex_scheduler_pipeline_step_removed_hosts_bucket{step="test_step",le="100.00000000000003"} 1
        cortex_scheduler_pipeline_step_removed_hosts_bucket{step="test_step",le="215.44346900318845"} 1
        cortex_scheduler_pipeline_step_removed_hosts_bucket{step="test_step",le="464.15888336127813"} 1
        cortex_scheduler_pipeline_step_removed_hosts_bucket{step="test_step",le="1000.0000000000006"} 1
        cortex_scheduler_pipeline_step_removed_hosts_bucket{step="test_step",le="+Inf"} 1
        cortex_scheduler_pipeline_step_removed_hosts_sum{step="test_step"} 1
        cortex_scheduler_pipeline_step_removed_hosts_count{step="test_step"} 1
    `)
	monitor.stepRemovedHostsObserver.WithLabelValues("test_step").Observe(1)
	err = testutil.GatherAndCompare(registry, expectedRemovedHostsObserver, "cortex_scheduler_pipeline_step_removed_hosts")
	if err != nil {
		t.Fatalf("stepRemovedHostsObserver test failed: %v", err)
	}

	// Test stepReorderingsObserver
	expectedReorderingsObserver := strings.NewReader(`
        # HELP cortex_scheduler_pipeline_step_reorderings_levenshtein Number of reorderings conducted by the scheduler step. Defined as the Levenshtein distance between the hosts going in and out of the scheduler pipeline.
        # TYPE cortex_scheduler_pipeline_step_reorderings_levenshtein histogram
        cortex_scheduler_pipeline_step_reorderings_levenshtein_bucket{step="test_step",le="0"} 0
        cortex_scheduler_pipeline_step_reorderings_levenshtein_bucket{step="test_step",le="1"} 1
        cortex_scheduler_pipeline_step_reorderings_levenshtein_bucket{step="test_step",le="2"} 1
        cortex_scheduler_pipeline_step_reorderings_levenshtein_bucket{step="test_step",le="3"} 1
        cortex_scheduler_pipeline_step_reorderings_levenshtein_bucket{step="test_step",le="4"} 1
        cortex_scheduler_pipeline_step_reorderings_levenshtein_bucket{step="test_step",le="5"} 1
        cortex_scheduler_pipeline_step_reorderings_levenshtein_bucket{step="test_step",le="6"} 1
        cortex_scheduler_pipeline_step_reorderings_levenshtein_bucket{step="test_step",le="7"} 1
        cortex_scheduler_pipeline_step_reorderings_levenshtein_bucket{step="test_step",le="8"} 1
        cortex_scheduler_pipeline_step_reorderings_levenshtein_bucket{step="test_step",le="9"} 1
        cortex_scheduler_pipeline_step_reorderings_levenshtein_bucket{step="test_step",le="10"} 1
        cortex_scheduler_pipeline_step_reorderings_levenshtein_bucket{step="test_step",le="11"} 1
        cortex_scheduler_pipeline_step_reorderings_levenshtein_bucket{step="test_step",le="12"} 1
        cortex_scheduler_pipeline_step_reorderings_levenshtein_bucket{step="test_step",le="13"} 1
        cortex_scheduler_pipeline_step_reorderings_levenshtein_bucket{step="test_step",le="14"} 1
        cortex_scheduler_pipeline_step_reorderings_levenshtein_bucket{step="test_step",le="15"} 1
        cortex_scheduler_pipeline_step_reorderings_levenshtein_bucket{step="test_step",le="16"} 1
        cortex_scheduler_pipeline_step_reorderings_levenshtein_bucket{step="test_step",le="17"} 1
        cortex_scheduler_pipeline_step_reorderings_levenshtein_bucket{step="test_step",le="18"} 1
        cortex_scheduler_pipeline_step_reorderings_levenshtein_bucket{step="test_step",le="19"} 1
        cortex_scheduler_pipeline_step_reorderings_levenshtein_bucket{step="test_step",le="20"} 1
        cortex_scheduler_pipeline_step_reorderings_levenshtein_bucket{step="test_step",le="21"} 1
        cortex_scheduler_pipeline_step_reorderings_levenshtein_bucket{step="test_step",le="22"} 1
        cortex_scheduler_pipeline_step_reorderings_levenshtein_bucket{step="test_step",le="23"} 1
        cortex_scheduler_pipeline_step_reorderings_levenshtein_bucket{step="test_step",le="24"} 1
        cortex_scheduler_pipeline_step_reorderings_levenshtein_bucket{step="test_step",le="+Inf"} 1
        cortex_scheduler_pipeline_step_reorderings_levenshtein_sum{step="test_step"} 1
        cortex_scheduler_pipeline_step_reorderings_levenshtein_count{step="test_step"} 1
    `)
	monitor.stepReorderingsObserver.WithLabelValues("test_step").Observe(1)
	err = testutil.GatherAndCompare(registry, expectedReorderingsObserver, "cortex_scheduler_pipeline_step_reorderings_levenshtein")
	if err != nil {
		t.Fatalf("stepReorderingsObserver test failed: %v", err)
	}

	// Test pipelineRunTimer
	expectedPipelineRunTimer := strings.NewReader(`
        # HELP cortex_scheduler_pipeline_run_duration_seconds Duration of scheduler pipeline run
        # TYPE cortex_scheduler_pipeline_run_duration_seconds histogram
        cortex_scheduler_pipeline_run_duration_seconds_bucket{le="0.005"} 1
        cortex_scheduler_pipeline_run_duration_seconds_bucket{le="0.01"} 1
        cortex_scheduler_pipeline_run_duration_seconds_bucket{le="0.025"} 1
        cortex_scheduler_pipeline_run_duration_seconds_bucket{le="0.05"} 1
        cortex_scheduler_pipeline_run_duration_seconds_bucket{le="0.1"} 1
        cortex_scheduler_pipeline_run_duration_seconds_bucket{le="0.25"} 1
        cortex_scheduler_pipeline_run_duration_seconds_bucket{le="0.5"} 1
        cortex_scheduler_pipeline_run_duration_seconds_bucket{le="1"} 1
        cortex_scheduler_pipeline_run_duration_seconds_bucket{le="2.5"} 1
        cortex_scheduler_pipeline_run_duration_seconds_bucket{le="5"} 1
        cortex_scheduler_pipeline_run_duration_seconds_bucket{le="10"} 1
        cortex_scheduler_pipeline_run_duration_seconds_bucket{le="+Inf"} 1
        cortex_scheduler_pipeline_run_duration_seconds_sum 0
        cortex_scheduler_pipeline_run_duration_seconds_count 1
    `)
	monitor.pipelineRunTimer.Observe(0)
	err = testutil.GatherAndCompare(registry, expectedPipelineRunTimer, "cortex_scheduler_pipeline_run_duration_seconds")
	if err != nil {
		t.Fatalf("pipelineRunTimer test failed: %v", err)
	}

	// Test requestCounter
	expectedRequestCounter := strings.NewReader(`
        # HELP cortex_scheduler_pipeline_requests_total Total number of requests processed by the scheduler.
        # TYPE cortex_scheduler_pipeline_requests_total counter
        cortex_scheduler_pipeline_requests_total{live="true",rebuild="true",resize="false",vmware="false"} 3
    `)
	monitor.requestCounter.WithLabelValues("true", "false", "true", "false").Add(3)
	err = testutil.GatherAndCompare(registry, expectedRequestCounter, "cortex_scheduler_pipeline_requests_total")
	if err != nil {
		t.Fatalf("requestCounter test failed: %v", err)
	}
}

func TestStepMonitorRun(t *testing.T) {
	runTimer := &testlibMonitoring.MockObserver{}
	removedHostsObserver := &testlibMonitoring.MockObserver{}
	reorderingsObserver := &testlibMonitoring.MockObserver{}
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
