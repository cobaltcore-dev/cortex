// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0
package extractor

import (
	"strings"
	"testing"

	"github.com/cobaltcore-dev/cortex/extractor/plugins"
	"github.com/cobaltcore-dev/cortex/internal/monitoring"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"
)

func TestFeatureExtractorMonitor(t *testing.T) {
	registry := &monitoring.Registry{Registry: prometheus.NewRegistry()}
	monitor := NewPipelineMonitor(registry)

	// Mock feature extractor
	mockExtractor := &mockFeatureExtractor{
		name: "mock_extractor",
		// Usually the features are a struct, but it doesn't matter for this test
		extractFunc: func() ([]plugins.Feature, error) {
			return []plugins.Feature{"1", "2"}, nil
		},
	}

	// Wrap the mock extractor with the monitor
	extractorMonitor := monitorFeatureExtractor(mockExtractor, monitor)

	// Test stepRunTimer
	expectedStepRunTimer := `
        # HELP cortex_feature_pipeline_step_run_duration_seconds Duration of feature pipeline step run
        # TYPE cortex_feature_pipeline_step_run_duration_seconds histogram
        cortex_feature_pipeline_step_run_duration_seconds_bucket{step="mock_extractor",le="0.005"} 1
        cortex_feature_pipeline_step_run_duration_seconds_bucket{step="mock_extractor",le="0.01"} 1
        cortex_feature_pipeline_step_run_duration_seconds_bucket{step="mock_extractor",le="0.025"} 1
        cortex_feature_pipeline_step_run_duration_seconds_bucket{step="mock_extractor",le="0.05"} 1
        cortex_feature_pipeline_step_run_duration_seconds_bucket{step="mock_extractor",le="0.1"} 1
        cortex_feature_pipeline_step_run_duration_seconds_bucket{step="mock_extractor",le="0.25"} 1
        cortex_feature_pipeline_step_run_duration_seconds_bucket{step="mock_extractor",le="0.5"} 1
        cortex_feature_pipeline_step_run_duration_seconds_bucket{step="mock_extractor",le="1"} 1
        cortex_feature_pipeline_step_run_duration_seconds_bucket{step="mock_extractor",le="2.5"} 1
        cortex_feature_pipeline_step_run_duration_seconds_bucket{step="mock_extractor",le="5"} 1
        cortex_feature_pipeline_step_run_duration_seconds_bucket{step="mock_extractor",le="10"} 1
        cortex_feature_pipeline_step_run_duration_seconds_bucket{step="mock_extractor",le="+Inf"} 1
        cortex_feature_pipeline_step_run_duration_seconds_sum{step="mock_extractor"} 0
        cortex_feature_pipeline_step_run_duration_seconds_count{step="mock_extractor"} 1
    `
	extractorMonitor.runTimer.Observe(0)
	err := testutil.GatherAndCompare(registry, strings.NewReader(expectedStepRunTimer), "cortex_feature_pipeline_step_run_duration_seconds")
	if err != nil {
		t.Fatalf("stepRunTimer test failed: %v", err)
	}

	// Test stepFeatureCounter
	expectedStepFeatureCounter := `
        # HELP cortex_feature_pipeline_step_features Number of features extracted by a feature pipeline step
        # TYPE cortex_feature_pipeline_step_features gauge
        cortex_feature_pipeline_step_features{step="mock_extractor"} 2
    `
	features, err := extractorMonitor.Extract()
	if err != nil {
		t.Fatalf("Extract() error = %v, want nil", err)
	}
	if len(features) != 2 {
		t.Fatalf("Extract() returned %d features, want 2", len(features))
	}
	err = testutil.GatherAndCompare(registry, strings.NewReader(expectedStepFeatureCounter), "cortex_feature_pipeline_step_features")
	if err != nil {
		t.Fatalf("stepFeatureCounter test failed: %v", err)
	}
}
