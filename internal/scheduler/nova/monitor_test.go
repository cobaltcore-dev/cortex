// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package nova

import (
	"log/slog"
	"os"
	"strings"
	"testing"

	"github.com/cobaltcore-dev/cortex/internal/monitoring"
	"github.com/cobaltcore-dev/cortex/internal/scheduler/nova/api"
	"github.com/cobaltcore-dev/cortex/internal/scheduler/nova/plugins"
	testlibMonitoring "github.com/cobaltcore-dev/cortex/testlib/monitoring"
	testlibAPI "github.com/cobaltcore-dev/cortex/testlib/scheduler/nova/api"
	testlibPlugins "github.com/cobaltcore-dev/cortex/testlib/scheduler/nova/plugins"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"
)

func TestSchedulerMonitor(t *testing.T) {
	registry := &monitoring.Registry{Registry: prometheus.NewRegistry()}
	monitor := NewSchedulerMonitor(registry)

	// Test stepRunTimer
	expectedStepRunTimer := strings.NewReader(`
        # HELP cortex_scheduler_nova_pipeline_step_run_duration_seconds Duration of scheduler pipeline step run
        # TYPE cortex_scheduler_nova_pipeline_step_run_duration_seconds histogram
        cortex_scheduler_nova_pipeline_step_run_duration_seconds_bucket{step="test_step",le="0.005"} 1
        cortex_scheduler_nova_pipeline_step_run_duration_seconds_bucket{step="test_step",le="0.01"} 1
        cortex_scheduler_nova_pipeline_step_run_duration_seconds_bucket{step="test_step",le="0.025"} 1
        cortex_scheduler_nova_pipeline_step_run_duration_seconds_bucket{step="test_step",le="0.05"} 1
        cortex_scheduler_nova_pipeline_step_run_duration_seconds_bucket{step="test_step",le="0.1"} 1
        cortex_scheduler_nova_pipeline_step_run_duration_seconds_bucket{step="test_step",le="0.25"} 1
        cortex_scheduler_nova_pipeline_step_run_duration_seconds_bucket{step="test_step",le="0.5"} 1
        cortex_scheduler_nova_pipeline_step_run_duration_seconds_bucket{step="test_step",le="1"} 1
        cortex_scheduler_nova_pipeline_step_run_duration_seconds_bucket{step="test_step",le="2.5"} 1
        cortex_scheduler_nova_pipeline_step_run_duration_seconds_bucket{step="test_step",le="5"} 1
        cortex_scheduler_nova_pipeline_step_run_duration_seconds_bucket{step="test_step",le="10"} 1
        cortex_scheduler_nova_pipeline_step_run_duration_seconds_bucket{step="test_step",le="+Inf"} 1
        cortex_scheduler_nova_pipeline_step_run_duration_seconds_sum{step="test_step"} 0
        cortex_scheduler_nova_pipeline_step_run_duration_seconds_count{step="test_step"} 1
    `)
	monitor.stepRunTimer.WithLabelValues("test_step").Observe(0)
	err := testutil.GatherAndCompare(registry, expectedStepRunTimer, "cortex_scheduler_nova_pipeline_step_run_duration_seconds")
	if err != nil {
		t.Fatalf("stepRunTimer test failed: %v", err)
	}

	// Test stepHostWeight
	expectedStepHostWeight := strings.NewReader(`
        # HELP cortex_scheduler_nova_pipeline_step_weight_modification Modification of host weight by scheduler pipeline step
        # TYPE cortex_scheduler_nova_pipeline_step_weight_modification gauge
        cortex_scheduler_nova_pipeline_step_weight_modification{host="test_host",step="test_step"} 42
    `)
	monitor.stepHostWeight.WithLabelValues("test_host", "test_step").Set(42)
	err = testutil.GatherAndCompare(registry, expectedStepHostWeight, "cortex_scheduler_nova_pipeline_step_weight_modification")
	if err != nil {
		t.Fatalf("stepHostWeight test failed: %v", err)
	}

	// Test stepRemovedHostsObserver
	expectedRemovedHostsObserver := strings.NewReader(`
        # HELP cortex_scheduler_nova_pipeline_step_removed_hosts Number of hosts removed by scheduler pipeline step
        # TYPE cortex_scheduler_nova_pipeline_step_removed_hosts histogram
        cortex_scheduler_nova_pipeline_step_removed_hosts_bucket{step="test_step",le="1"} 1
        cortex_scheduler_nova_pipeline_step_removed_hosts_bucket{step="test_step",le="2.154434690031884"} 1
        cortex_scheduler_nova_pipeline_step_removed_hosts_bucket{step="test_step",le="4.641588833612779"} 1
        cortex_scheduler_nova_pipeline_step_removed_hosts_bucket{step="test_step",le="10.000000000000002"} 1
        cortex_scheduler_nova_pipeline_step_removed_hosts_bucket{step="test_step",le="21.544346900318843"} 1
        cortex_scheduler_nova_pipeline_step_removed_hosts_bucket{step="test_step",le="46.4158883361278"} 1
        cortex_scheduler_nova_pipeline_step_removed_hosts_bucket{step="test_step",le="100.00000000000003"} 1
        cortex_scheduler_nova_pipeline_step_removed_hosts_bucket{step="test_step",le="215.44346900318845"} 1
        cortex_scheduler_nova_pipeline_step_removed_hosts_bucket{step="test_step",le="464.15888336127813"} 1
        cortex_scheduler_nova_pipeline_step_removed_hosts_bucket{step="test_step",le="1000.0000000000006"} 1
        cortex_scheduler_nova_pipeline_step_removed_hosts_bucket{step="test_step",le="+Inf"} 1
        cortex_scheduler_nova_pipeline_step_removed_hosts_sum{step="test_step"} 1
        cortex_scheduler_nova_pipeline_step_removed_hosts_count{step="test_step"} 1
    `)
	monitor.stepRemovedHostsObserver.WithLabelValues("test_step").Observe(1)
	err = testutil.GatherAndCompare(registry, expectedRemovedHostsObserver, "cortex_scheduler_nova_pipeline_step_removed_hosts")
	if err != nil {
		t.Fatalf("stepRemovedHostsObserver test failed: %v", err)
	}

	// Test pipelineRunTimer
	expectedPipelineRunTimer := strings.NewReader(`
        # HELP cortex_scheduler_nova_pipeline_run_duration_seconds Duration of scheduler pipeline run
        # TYPE cortex_scheduler_nova_pipeline_run_duration_seconds histogram
        cortex_scheduler_nova_pipeline_run_duration_seconds_bucket{le="0.005"} 1
        cortex_scheduler_nova_pipeline_run_duration_seconds_bucket{le="0.01"} 1
        cortex_scheduler_nova_pipeline_run_duration_seconds_bucket{le="0.025"} 1
        cortex_scheduler_nova_pipeline_run_duration_seconds_bucket{le="0.05"} 1
        cortex_scheduler_nova_pipeline_run_duration_seconds_bucket{le="0.1"} 1
        cortex_scheduler_nova_pipeline_run_duration_seconds_bucket{le="0.25"} 1
        cortex_scheduler_nova_pipeline_run_duration_seconds_bucket{le="0.5"} 1
        cortex_scheduler_nova_pipeline_run_duration_seconds_bucket{le="1"} 1
        cortex_scheduler_nova_pipeline_run_duration_seconds_bucket{le="2.5"} 1
        cortex_scheduler_nova_pipeline_run_duration_seconds_bucket{le="5"} 1
        cortex_scheduler_nova_pipeline_run_duration_seconds_bucket{le="10"} 1
        cortex_scheduler_nova_pipeline_run_duration_seconds_bucket{le="+Inf"} 1
        cortex_scheduler_nova_pipeline_run_duration_seconds_sum 0
        cortex_scheduler_nova_pipeline_run_duration_seconds_count 1
    `)
	monitor.pipelineRunTimer.Observe(0)
	err = testutil.GatherAndCompare(registry, expectedPipelineRunTimer, "cortex_scheduler_nova_pipeline_run_duration_seconds")
	if err != nil {
		t.Fatalf("pipelineRunTimer test failed: %v", err)
	}

	// Test requestCounter
	expectedRequestCounter := strings.NewReader(`
        # HELP cortex_scheduler_nova_pipeline_requests_total Total number of requests processed by the scheduler.
        # TYPE cortex_scheduler_nova_pipeline_requests_total counter
        cortex_scheduler_nova_pipeline_requests_total{live="true",rebuild="true",resize="false",vmware="false"} 3
    `)
	monitor.requestCounter.WithLabelValues("true", "false", "true", "false").Add(3)
	err = testutil.GatherAndCompare(registry, expectedRequestCounter, "cortex_scheduler_nova_pipeline_requests_total")
	if err != nil {
		t.Fatalf("requestCounter test failed: %v", err)
	}
}

func TestStepMonitorRun(t *testing.T) {
	runTimer := &testlibMonitoring.MockObserver{}
	removedHostsObserver := &testlibMonitoring.MockObserver{}
	monitor := &StepMonitor{
		Step: &testlibPlugins.MockStep{
			Name: "mock_step",
			RunFunc: func(traceLog *slog.Logger, request api.Request) (*plugins.StepResult, error) {
				return &plugins.StepResult{
					Activations: map[string]float64{"host1": 0.1, "host2": 1.0, "host3": 0.0},
				}, nil
			},
		},
		runTimer:             runTimer,
		stepHostWeight:       nil,
		removedHostsObserver: removedHostsObserver,
	}
	request := &testlibAPI.MockRequest{
		Hosts:   []string{"host1", "host2", "host3"},
		Weights: map[string]float64{"host1": 0.2, "host2": 0.1, "host3": 0.0},
	}
	if _, err := monitor.Run(slog.Default(), request); err != nil {
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
