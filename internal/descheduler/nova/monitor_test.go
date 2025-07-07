// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package nova

import (
	"strings"
	"testing"

	"github.com/cobaltcore-dev/cortex/internal/monitoring"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"
)

func TestDeschedulerMonitor(t *testing.T) {
	registry := &monitoring.Registry{Registry: prometheus.NewRegistry()}
	monitor := NewPipelineMonitor(registry)

	// Test stepRunTimer
	expectedStepRunTimer := strings.NewReader(`
        # HELP cortex_descheduler_pipeline_step_run_duration_seconds Duration of descheduler pipeline step run
        # TYPE cortex_descheduler_pipeline_step_run_duration_seconds histogram
        cortex_descheduler_pipeline_step_run_duration_seconds_bucket{step="test_step",le="0.001"} 1
        cortex_descheduler_pipeline_step_run_duration_seconds_bucket{step="test_step",le="0.002"} 1
        cortex_descheduler_pipeline_step_run_duration_seconds_bucket{step="test_step",le="0.004"} 1
        cortex_descheduler_pipeline_step_run_duration_seconds_bucket{step="test_step",le="0.008"} 1
        cortex_descheduler_pipeline_step_run_duration_seconds_bucket{step="test_step",le="0.016"} 1
        cortex_descheduler_pipeline_step_run_duration_seconds_bucket{step="test_step",le="0.032"} 1
        cortex_descheduler_pipeline_step_run_duration_seconds_bucket{step="test_step",le="0.064"} 1
        cortex_descheduler_pipeline_step_run_duration_seconds_bucket{step="test_step",le="0.128"} 1
        cortex_descheduler_pipeline_step_run_duration_seconds_bucket{step="test_step",le="0.256"} 1
        cortex_descheduler_pipeline_step_run_duration_seconds_bucket{step="test_step",le="0.512"} 1
        cortex_descheduler_pipeline_step_run_duration_seconds_bucket{step="test_step",le="1.024"} 1
        cortex_descheduler_pipeline_step_run_duration_seconds_bucket{step="test_step",le="2.048"} 1
        cortex_descheduler_pipeline_step_run_duration_seconds_bucket{step="test_step",le="4.096"} 1
        cortex_descheduler_pipeline_step_run_duration_seconds_bucket{step="test_step",le="8.192"} 1
        cortex_descheduler_pipeline_step_run_duration_seconds_bucket{step="test_step",le="16.384"} 1
        cortex_descheduler_pipeline_step_run_duration_seconds_bucket{step="test_step",le="32.768"} 1
        cortex_descheduler_pipeline_step_run_duration_seconds_bucket{step="test_step",le="65.536"} 1
        cortex_descheduler_pipeline_step_run_duration_seconds_bucket{step="test_step",le="131.072"} 1
        cortex_descheduler_pipeline_step_run_duration_seconds_bucket{step="test_step",le="262.144"} 1
        cortex_descheduler_pipeline_step_run_duration_seconds_bucket{step="test_step",le="524.288"} 1
        cortex_descheduler_pipeline_step_run_duration_seconds_bucket{step="test_step",le="1048.576"} 1
        cortex_descheduler_pipeline_step_run_duration_seconds_bucket{step="test_step",le="+Inf"} 1
        cortex_descheduler_pipeline_step_run_duration_seconds_sum{step="test_step"} 0
        cortex_descheduler_pipeline_step_run_duration_seconds_count{step="test_step"} 1
    `)
	monitor.stepRunTimer.WithLabelValues("test_step").Observe(0)
	err := testutil.GatherAndCompare(registry, expectedStepRunTimer, "cortex_descheduler_pipeline_step_run_duration_seconds")
	if err != nil {
		t.Fatalf("stepRunTimer test failed: %v", err)
	}

	// Test stepDeschedulingCounter
	expectedStepDeschedulingCounter := strings.NewReader(`
        # HELP cortex_descheduler_pipeline_step_vms_descheduled Number of vms descheduled by a descheduler pipeline step
        # TYPE cortex_descheduler_pipeline_step_vms_descheduled gauge
        cortex_descheduler_pipeline_step_vms_descheduled{step="test_step"} 3
    `)
	monitor.stepDeschedulingCounter.WithLabelValues("test_step").Set(3)
	err = testutil.GatherAndCompare(registry, expectedStepDeschedulingCounter, "cortex_descheduler_pipeline_step_vms_descheduled")
	if err != nil {
		t.Fatalf("stepDeschedulingCounter test failed: %v", err)
	}

	// Test pipelineRunTimer
	expectedPipelineRunTimer := strings.NewReader(`
        # HELP cortex_descheduler_pipeline_run_duration_seconds Duration of descheduler pipeline run
        # TYPE cortex_descheduler_pipeline_run_duration_seconds histogram
        cortex_descheduler_pipeline_run_duration_seconds_bucket{le="0.005"} 1
        cortex_descheduler_pipeline_run_duration_seconds_bucket{le="0.01"} 1
        cortex_descheduler_pipeline_run_duration_seconds_bucket{le="0.025"} 1
        cortex_descheduler_pipeline_run_duration_seconds_bucket{le="0.05"} 1
        cortex_descheduler_pipeline_run_duration_seconds_bucket{le="0.1"} 1
        cortex_descheduler_pipeline_run_duration_seconds_bucket{le="0.25"} 1
        cortex_descheduler_pipeline_run_duration_seconds_bucket{le="0.5"} 1
        cortex_descheduler_pipeline_run_duration_seconds_bucket{le="1"} 1
        cortex_descheduler_pipeline_run_duration_seconds_bucket{le="2.5"} 1
        cortex_descheduler_pipeline_run_duration_seconds_bucket{le="5"} 1
        cortex_descheduler_pipeline_run_duration_seconds_bucket{le="10"} 1
        cortex_descheduler_pipeline_run_duration_seconds_bucket{le="+Inf"} 1
        cortex_descheduler_pipeline_run_duration_seconds_sum 0
        cortex_descheduler_pipeline_run_duration_seconds_count 1
    `)
	monitor.pipelineRunTimer.Observe(0)
	err = testutil.GatherAndCompare(registry, expectedPipelineRunTimer, "cortex_descheduler_pipeline_run_duration_seconds")
	if err != nil {
		t.Fatalf("pipelineRunTimer test failed: %v", err)
	}

	// Test deschedulingRunTimer
	expectedDeschedulingRunTimer := strings.NewReader(`
        # HELP cortex_descheduler_pipeline_vm_descheduling_duration_seconds Duration of descheduling a VM in the descheduler pipeline
        # TYPE cortex_descheduler_pipeline_vm_descheduling_duration_seconds histogram
        cortex_descheduler_pipeline_vm_descheduling_duration_seconds_bucket{error="",skipped="false",source_host="host1",target_host="host2",vm_id="vm123",le="0.001"} 1
        cortex_descheduler_pipeline_vm_descheduling_duration_seconds_bucket{error="",skipped="false",source_host="host1",target_host="host2",vm_id="vm123",le="0.002"} 1
        cortex_descheduler_pipeline_vm_descheduling_duration_seconds_bucket{error="",skipped="false",source_host="host1",target_host="host2",vm_id="vm123",le="0.004"} 1
        cortex_descheduler_pipeline_vm_descheduling_duration_seconds_bucket{error="",skipped="false",source_host="host1",target_host="host2",vm_id="vm123",le="0.008"} 1
        cortex_descheduler_pipeline_vm_descheduling_duration_seconds_bucket{error="",skipped="false",source_host="host1",target_host="host2",vm_id="vm123",le="0.016"} 1
        cortex_descheduler_pipeline_vm_descheduling_duration_seconds_bucket{error="",skipped="false",source_host="host1",target_host="host2",vm_id="vm123",le="0.032"} 1
        cortex_descheduler_pipeline_vm_descheduling_duration_seconds_bucket{error="",skipped="false",source_host="host1",target_host="host2",vm_id="vm123",le="0.064"} 1
        cortex_descheduler_pipeline_vm_descheduling_duration_seconds_bucket{error="",skipped="false",source_host="host1",target_host="host2",vm_id="vm123",le="0.128"} 1
        cortex_descheduler_pipeline_vm_descheduling_duration_seconds_bucket{error="",skipped="false",source_host="host1",target_host="host2",vm_id="vm123",le="0.256"} 1
        cortex_descheduler_pipeline_vm_descheduling_duration_seconds_bucket{error="",skipped="false",source_host="host1",target_host="host2",vm_id="vm123",le="0.512"} 1
        cortex_descheduler_pipeline_vm_descheduling_duration_seconds_bucket{error="",skipped="false",source_host="host1",target_host="host2",vm_id="vm123",le="1.024"} 1
        cortex_descheduler_pipeline_vm_descheduling_duration_seconds_bucket{error="",skipped="false",source_host="host1",target_host="host2",vm_id="vm123",le="2.048"} 1
        cortex_descheduler_pipeline_vm_descheduling_duration_seconds_bucket{error="",skipped="false",source_host="host1",target_host="host2",vm_id="vm123",le="4.096"} 1
        cortex_descheduler_pipeline_vm_descheduling_duration_seconds_bucket{error="",skipped="false",source_host="host1",target_host="host2",vm_id="vm123",le="8.192"} 1
        cortex_descheduler_pipeline_vm_descheduling_duration_seconds_bucket{error="",skipped="false",source_host="host1",target_host="host2",vm_id="vm123",le="16.384"} 1
        cortex_descheduler_pipeline_vm_descheduling_duration_seconds_bucket{error="",skipped="false",source_host="host1",target_host="host2",vm_id="vm123",le="32.768"} 1
        cortex_descheduler_pipeline_vm_descheduling_duration_seconds_bucket{error="",skipped="false",source_host="host1",target_host="host2",vm_id="vm123",le="65.536"} 1
        cortex_descheduler_pipeline_vm_descheduling_duration_seconds_bucket{error="",skipped="false",source_host="host1",target_host="host2",vm_id="vm123",le="131.072"} 1
        cortex_descheduler_pipeline_vm_descheduling_duration_seconds_bucket{error="",skipped="false",source_host="host1",target_host="host2",vm_id="vm123",le="262.144"} 1
        cortex_descheduler_pipeline_vm_descheduling_duration_seconds_bucket{error="",skipped="false",source_host="host1",target_host="host2",vm_id="vm123",le="524.288"} 1
        cortex_descheduler_pipeline_vm_descheduling_duration_seconds_bucket{error="",skipped="false",source_host="host1",target_host="host2",vm_id="vm123",le="1048.576"} 1
        cortex_descheduler_pipeline_vm_descheduling_duration_seconds_bucket{error="",skipped="false",source_host="host1",target_host="host2",vm_id="vm123",le="+Inf"} 1
        cortex_descheduler_pipeline_vm_descheduling_duration_seconds_sum{error="",skipped="false",source_host="host1",target_host="host2",vm_id="vm123"} 0
        cortex_descheduler_pipeline_vm_descheduling_duration_seconds_count{error="",skipped="false",source_host="host1",target_host="host2",vm_id="vm123"} 1
    `)
	monitor.deschedulingRunTimer.WithLabelValues("", "false", "host1", "host2", "vm123").Observe(0)
	err = testutil.GatherAndCompare(registry, expectedDeschedulingRunTimer, "cortex_descheduler_pipeline_vm_descheduling_duration_seconds")
	if err != nil {
		t.Fatalf("deschedulingRunTimer test failed: %v", err)
	}
}
