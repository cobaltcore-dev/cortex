// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package lib

import (
	"strings"
	"testing"

	"github.com/cobaltcore-dev/cortex/internal/monitoring"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"
)

func TestSchedulerMonitor(t *testing.T) {
	registry := &monitoring.Registry{Registry: prometheus.NewRegistry()}
	monitor := NewPipelineMonitor(registry).SubPipeline("test")

	// Test stepRunTimer
	expectedStepRunTimer := strings.NewReader(`
        # HELP cortex_scheduler_pipeline_step_run_duration_seconds Duration of scheduler pipeline step run
        # TYPE cortex_scheduler_pipeline_step_run_duration_seconds histogram
        cortex_scheduler_pipeline_step_run_duration_seconds_bucket{alias="test_alias",pipeline="test",step="test_step",le="0.005"} 1
        cortex_scheduler_pipeline_step_run_duration_seconds_bucket{alias="test_alias",pipeline="test",step="test_step",le="0.01"} 1
        cortex_scheduler_pipeline_step_run_duration_seconds_bucket{alias="test_alias",pipeline="test",step="test_step",le="0.025"} 1
        cortex_scheduler_pipeline_step_run_duration_seconds_bucket{alias="test_alias",pipeline="test",step="test_step",le="0.05"} 1
        cortex_scheduler_pipeline_step_run_duration_seconds_bucket{alias="test_alias",pipeline="test",step="test_step",le="0.1"} 1
        cortex_scheduler_pipeline_step_run_duration_seconds_bucket{alias="test_alias",pipeline="test",step="test_step",le="0.25"} 1
        cortex_scheduler_pipeline_step_run_duration_seconds_bucket{alias="test_alias",pipeline="test",step="test_step",le="0.5"} 1
        cortex_scheduler_pipeline_step_run_duration_seconds_bucket{alias="test_alias",pipeline="test",step="test_step",le="1"} 1
        cortex_scheduler_pipeline_step_run_duration_seconds_bucket{alias="test_alias",pipeline="test",step="test_step",le="2.5"} 1
        cortex_scheduler_pipeline_step_run_duration_seconds_bucket{alias="test_alias",pipeline="test",step="test_step",le="5"} 1
        cortex_scheduler_pipeline_step_run_duration_seconds_bucket{alias="test_alias",pipeline="test",step="test_step",le="10"} 1
        cortex_scheduler_pipeline_step_run_duration_seconds_bucket{alias="test_alias",pipeline="test",step="test_step",le="+Inf"} 1
        cortex_scheduler_pipeline_step_run_duration_seconds_sum{alias="test_alias",pipeline="test",step="test_step"} 0
        cortex_scheduler_pipeline_step_run_duration_seconds_count{alias="test_alias",pipeline="test",step="test_step"} 1
    `)
	monitor.stepRunTimer.WithLabelValues("test", "test_step", "test_alias").Observe(0)
	err := testutil.GatherAndCompare(registry, expectedStepRunTimer, "cortex_scheduler_pipeline_step_run_duration_seconds")
	if err != nil {
		t.Fatalf("stepRunTimer test failed: %v", err)
	}

	// Test stepSubjectWeight
	expectedStepSubjectWeight := strings.NewReader(`
        # HELP cortex_scheduler_pipeline_step_weight_modification Modification of subject weight by scheduler pipeline step
        # TYPE cortex_scheduler_pipeline_step_weight_modification gauge
        cortex_scheduler_pipeline_step_weight_modification{alias="test_alias",pipeline="test",step="test_step",subject="test_subject"} 42
    `)
	monitor.stepSubjectWeight.WithLabelValues("test", "test_subject", "test_step", "test_alias").Set(42)
	err = testutil.GatherAndCompare(registry, expectedStepSubjectWeight, "cortex_scheduler_pipeline_step_weight_modification")
	if err != nil {
		t.Fatalf("stepSubjectWeight test failed: %v", err)
	}

	// Test stepRemovedSubjectsObserver
	expectedRemovedSubjectsObserver := strings.NewReader(`
        # HELP cortex_scheduler_pipeline_step_removed_subjects Number of subjects removed by scheduler pipeline step
        # TYPE cortex_scheduler_pipeline_step_removed_subjects histogram
        cortex_scheduler_pipeline_step_removed_subjects_bucket{alias="test_alias",pipeline="test",step="test_step",le="1"} 1
        cortex_scheduler_pipeline_step_removed_subjects_bucket{alias="test_alias",pipeline="test",step="test_step",le="2.154434690031884"} 1
        cortex_scheduler_pipeline_step_removed_subjects_bucket{alias="test_alias",pipeline="test",step="test_step",le="4.641588833612779"} 1
        cortex_scheduler_pipeline_step_removed_subjects_bucket{alias="test_alias",pipeline="test",step="test_step",le="10.000000000000002"} 1
        cortex_scheduler_pipeline_step_removed_subjects_bucket{alias="test_alias",pipeline="test",step="test_step",le="21.544346900318843"} 1
        cortex_scheduler_pipeline_step_removed_subjects_bucket{alias="test_alias",pipeline="test",step="test_step",le="46.4158883361278"} 1
        cortex_scheduler_pipeline_step_removed_subjects_bucket{alias="test_alias",pipeline="test",step="test_step",le="100.00000000000003"} 1
        cortex_scheduler_pipeline_step_removed_subjects_bucket{alias="test_alias",pipeline="test",step="test_step",le="215.44346900318845"} 1
        cortex_scheduler_pipeline_step_removed_subjects_bucket{alias="test_alias",pipeline="test",step="test_step",le="464.15888336127813"} 1
        cortex_scheduler_pipeline_step_removed_subjects_bucket{alias="test_alias",pipeline="test",step="test_step",le="1000.0000000000006"} 1
        cortex_scheduler_pipeline_step_removed_subjects_bucket{alias="test_alias",pipeline="test",step="test_step",le="+Inf"} 1
        cortex_scheduler_pipeline_step_removed_subjects_sum{alias="test_alias",pipeline="test",step="test_step"} 1
        cortex_scheduler_pipeline_step_removed_subjects_count{alias="test_alias",pipeline="test",step="test_step"} 1
    `)
	monitor.stepRemovedSubjectsObserver.WithLabelValues("test", "test_step", "test_alias").Observe(1)
	err = testutil.GatherAndCompare(registry, expectedRemovedSubjectsObserver, "cortex_scheduler_pipeline_step_removed_subjects")
	if err != nil {
		t.Fatalf("stepRemovedSubjectsObserver test failed: %v", err)
	}

	// Test pipelineRunTimer
	expectedPipelineRunTimer := strings.NewReader(`
        # HELP cortex_scheduler_pipeline_run_duration_seconds Duration of scheduler pipeline run
        # TYPE cortex_scheduler_pipeline_run_duration_seconds histogram
        cortex_scheduler_pipeline_run_duration_seconds_bucket{pipeline="test",le="0.005"} 1
        cortex_scheduler_pipeline_run_duration_seconds_bucket{pipeline="test",le="0.01"} 1
        cortex_scheduler_pipeline_run_duration_seconds_bucket{pipeline="test",le="0.025"} 1
        cortex_scheduler_pipeline_run_duration_seconds_bucket{pipeline="test",le="0.05"} 1
        cortex_scheduler_pipeline_run_duration_seconds_bucket{pipeline="test",le="0.1"} 1
        cortex_scheduler_pipeline_run_duration_seconds_bucket{pipeline="test",le="0.25"} 1
        cortex_scheduler_pipeline_run_duration_seconds_bucket{pipeline="test",le="0.5"} 1
        cortex_scheduler_pipeline_run_duration_seconds_bucket{pipeline="test",le="1"} 1
        cortex_scheduler_pipeline_run_duration_seconds_bucket{pipeline="test",le="2.5"} 1
        cortex_scheduler_pipeline_run_duration_seconds_bucket{pipeline="test",le="5"} 1
        cortex_scheduler_pipeline_run_duration_seconds_bucket{pipeline="test",le="10"} 1
        cortex_scheduler_pipeline_run_duration_seconds_bucket{pipeline="test",le="+Inf"} 1
        cortex_scheduler_pipeline_run_duration_seconds_sum{pipeline="test"} 0
        cortex_scheduler_pipeline_run_duration_seconds_count{pipeline="test"} 1
    `)
	monitor.pipelineRunTimer.WithLabelValues("test").Observe(0)
	err = testutil.GatherAndCompare(registry, expectedPipelineRunTimer, "cortex_scheduler_pipeline_run_duration_seconds")
	if err != nil {
		t.Fatalf("pipelineRunTimer test failed: %v", err)
	}

	// Test requestCounter
	expectedRequestCounter := strings.NewReader(`
        # HELP cortex_scheduler_pipeline_requests_total Total number of requests processed by the scheduler.
        # TYPE cortex_scheduler_pipeline_requests_total counter
        cortex_scheduler_pipeline_requests_total{pipeline="test"} 3
    `)
	monitor.requestCounter.WithLabelValues("test").Add(3)
	err = testutil.GatherAndCompare(registry, expectedRequestCounter, "cortex_scheduler_pipeline_requests_total")
	if err != nil {
		t.Fatalf("requestCounter test failed: %v", err)
	}
}
