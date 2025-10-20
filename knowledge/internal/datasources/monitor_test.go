// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package datasources

import (
	"strings"
	"testing"

	"github.com/cobaltcore-dev/cortex/lib/monitoring"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"
)

func TestMonitor(t *testing.T) {
	registry := &monitoring.Registry{Registry: prometheus.NewRegistry()}
	monitor := NewSyncMonitor(registry)

	// Test PipelineRunTimer
	expectedRunTimer := strings.NewReader(`
        # HELP cortex_sync_run_duration_seconds Duration of sync run
        # TYPE cortex_sync_run_duration_seconds histogram
        cortex_sync_run_duration_seconds_bucket{datasource="test_step",le="0.001"} 1
        cortex_sync_run_duration_seconds_bucket{datasource="test_step",le="0.002"} 1
        cortex_sync_run_duration_seconds_bucket{datasource="test_step",le="0.004"} 1
        cortex_sync_run_duration_seconds_bucket{datasource="test_step",le="0.008"} 1
        cortex_sync_run_duration_seconds_bucket{datasource="test_step",le="0.016"} 1
        cortex_sync_run_duration_seconds_bucket{datasource="test_step",le="0.032"} 1
        cortex_sync_run_duration_seconds_bucket{datasource="test_step",le="0.064"} 1
        cortex_sync_run_duration_seconds_bucket{datasource="test_step",le="0.128"} 1
        cortex_sync_run_duration_seconds_bucket{datasource="test_step",le="0.256"} 1
        cortex_sync_run_duration_seconds_bucket{datasource="test_step",le="0.512"} 1
        cortex_sync_run_duration_seconds_bucket{datasource="test_step",le="1.024"} 1
        cortex_sync_run_duration_seconds_bucket{datasource="test_step",le="2.048"} 1
        cortex_sync_run_duration_seconds_bucket{datasource="test_step",le="4.096"} 1
        cortex_sync_run_duration_seconds_bucket{datasource="test_step",le="8.192"} 1
        cortex_sync_run_duration_seconds_bucket{datasource="test_step",le="16.384"} 1
        cortex_sync_run_duration_seconds_bucket{datasource="test_step",le="32.768"} 1
        cortex_sync_run_duration_seconds_bucket{datasource="test_step",le="65.536"} 1
        cortex_sync_run_duration_seconds_bucket{datasource="test_step",le="131.072"} 1
        cortex_sync_run_duration_seconds_bucket{datasource="test_step",le="262.144"} 1
        cortex_sync_run_duration_seconds_bucket{datasource="test_step",le="524.288"} 1
        cortex_sync_run_duration_seconds_bucket{datasource="test_step",le="1048.576"} 1
        cortex_sync_run_duration_seconds_bucket{datasource="test_step",le="+Inf"} 1
        cortex_sync_run_duration_seconds_sum{datasource="test_step"} 0
        cortex_sync_run_duration_seconds_count{datasource="test_step"} 1
    `)
	monitor.PipelineRunTimer.WithLabelValues("test_step").Observe(0)
	err := testutil.GatherAndCompare(registry, expectedRunTimer, "cortex_sync_run_duration_seconds")
	if err != nil {
		t.Fatalf("PipelineRunTimer test failed: %v", err)
	}

	// Test PipelineObjectsGauge
	expectedObjectsGauge := strings.NewReader(`
        # HELP cortex_sync_objects Number of objects synced
        # TYPE cortex_sync_objects gauge
        cortex_sync_objects{datasource="test_step"} 42
    `)
	monitor.PipelineObjectsGauge.WithLabelValues("test_step").Set(42)
	err = testutil.GatherAndCompare(registry, expectedObjectsGauge, "cortex_sync_objects")
	if err != nil {
		t.Fatalf("PipelineObjectsGauge test failed: %v", err)
	}

	// Test PipelineRequestTimer
	expectedRequestTimer := strings.NewReader(`
        # HELP cortex_sync_request_duration_seconds Duration of sync request
        # TYPE cortex_sync_request_duration_seconds histogram
        cortex_sync_request_duration_seconds_bucket{datasource="test_step",le="0.005"} 1
        cortex_sync_request_duration_seconds_bucket{datasource="test_step",le="0.01"} 1
        cortex_sync_request_duration_seconds_bucket{datasource="test_step",le="0.025"} 1
        cortex_sync_request_duration_seconds_bucket{datasource="test_step",le="0.05"} 1
        cortex_sync_request_duration_seconds_bucket{datasource="test_step",le="0.1"} 1
        cortex_sync_request_duration_seconds_bucket{datasource="test_step",le="0.25"} 1
        cortex_sync_request_duration_seconds_bucket{datasource="test_step",le="0.5"} 1
        cortex_sync_request_duration_seconds_bucket{datasource="test_step",le="1"} 1
        cortex_sync_request_duration_seconds_bucket{datasource="test_step",le="2.5"} 1
        cortex_sync_request_duration_seconds_bucket{datasource="test_step",le="5"} 1
        cortex_sync_request_duration_seconds_bucket{datasource="test_step",le="10"} 1
        cortex_sync_request_duration_seconds_bucket{datasource="test_step",le="+Inf"} 1
        cortex_sync_request_duration_seconds_sum{datasource="test_step"} 0
        cortex_sync_request_duration_seconds_count{datasource="test_step"} 1
    `)
	monitor.PipelineRequestTimer.WithLabelValues("test_step").Observe(0)
	err = testutil.GatherAndCompare(registry, expectedRequestTimer, "cortex_sync_request_duration_seconds")
	if err != nil {
		t.Fatalf("PipelineRequestTimer test failed: %v", err)
	}

	// Test PipelineRequestProcessedCounter
	expectedRequestCounter := strings.NewReader(`
        # HELP cortex_sync_request_processed_total Number of processed sync requests
        # TYPE cortex_sync_request_processed_total counter
        cortex_sync_request_processed_total{datasource="test_step"} 3
    `)
	monitor.PipelineRequestProcessedCounter.WithLabelValues("test_step").Add(3)
	err = testutil.GatherAndCompare(registry, expectedRequestCounter, "cortex_sync_request_processed_total")
	if err != nil {
		t.Fatalf("PipelineRequestProcessedCounter test failed: %v", err)
	}
}
