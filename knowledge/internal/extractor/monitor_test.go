// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0
package extractor

import (
	"strings"
	"testing"

	"github.com/cobaltcore-dev/cortex/knowledge/api/v1alpha1"
	"github.com/cobaltcore-dev/cortex/knowledge/internal/extractor/plugins"
	"github.com/cobaltcore-dev/cortex/lib/db"
	"github.com/cobaltcore-dev/cortex/lib/monitoring"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"
)

type mockFeatureExtractor struct {
	plugins.FeatureExtractor
	extractFunc func() ([]plugins.Feature, error)
}

func (m *mockFeatureExtractor) Init(datasourceDB *db.DB, extractorDB *db.DB, spec v1alpha1.KnowledgeSpec) error {
	return nil
}

func (m *mockFeatureExtractor) Extract() ([]plugins.Feature, error) {
	return m.extractFunc()
}

func (m *mockFeatureExtractor) NotifySkip() {
	// No-op for mock
}

func TestFeatureExtractorMonitor(t *testing.T) {
	registry := &monitoring.Registry{Registry: prometheus.NewRegistry()}
	monitor := NewPipelineMonitor(registry)

	// Mock feature extractor
	mockExtractor := &mockFeatureExtractor{
		// Usually the features are a struct, but it doesn't matter for this test
		extractFunc: func() ([]plugins.Feature, error) {
			return []plugins.Feature{"1", "2"}, nil
		},
	}

	// Wrap the mock extractor with the monitor
	extractorMonitor := monitorFeatureExtractor("mock_extractor", mockExtractor, monitor)

	// Test stepRunTimer
	expectedStepRunTimer := `
        # HELP cortex_feature_pipeline_step_run_duration_seconds Duration of feature pipeline step run
        # TYPE cortex_feature_pipeline_step_run_duration_seconds histogram
        cortex_feature_pipeline_step_run_duration_seconds_bucket{step="mock_extractor",le="0.001"} 1
        cortex_feature_pipeline_step_run_duration_seconds_bucket{step="mock_extractor",le="0.002"} 1
        cortex_feature_pipeline_step_run_duration_seconds_bucket{step="mock_extractor",le="0.004"} 1
        cortex_feature_pipeline_step_run_duration_seconds_bucket{step="mock_extractor",le="0.008"} 1
        cortex_feature_pipeline_step_run_duration_seconds_bucket{step="mock_extractor",le="0.016"} 1
        cortex_feature_pipeline_step_run_duration_seconds_bucket{step="mock_extractor",le="0.032"} 1
        cortex_feature_pipeline_step_run_duration_seconds_bucket{step="mock_extractor",le="0.064"} 1
        cortex_feature_pipeline_step_run_duration_seconds_bucket{step="mock_extractor",le="0.128"} 1
        cortex_feature_pipeline_step_run_duration_seconds_bucket{step="mock_extractor",le="0.256"} 1
        cortex_feature_pipeline_step_run_duration_seconds_bucket{step="mock_extractor",le="0.512"} 1
        cortex_feature_pipeline_step_run_duration_seconds_bucket{step="mock_extractor",le="1.024"} 1
        cortex_feature_pipeline_step_run_duration_seconds_bucket{step="mock_extractor",le="2.048"} 1
        cortex_feature_pipeline_step_run_duration_seconds_bucket{step="mock_extractor",le="4.096"} 1
        cortex_feature_pipeline_step_run_duration_seconds_bucket{step="mock_extractor",le="8.192"} 1
        cortex_feature_pipeline_step_run_duration_seconds_bucket{step="mock_extractor",le="16.384"} 1
        cortex_feature_pipeline_step_run_duration_seconds_bucket{step="mock_extractor",le="32.768"} 1
        cortex_feature_pipeline_step_run_duration_seconds_bucket{step="mock_extractor",le="65.536"} 1
        cortex_feature_pipeline_step_run_duration_seconds_bucket{step="mock_extractor",le="131.072"} 1
        cortex_feature_pipeline_step_run_duration_seconds_bucket{step="mock_extractor",le="262.144"} 1
        cortex_feature_pipeline_step_run_duration_seconds_bucket{step="mock_extractor",le="524.288"} 1
        cortex_feature_pipeline_step_run_duration_seconds_bucket{step="mock_extractor",le="1048.576"} 1
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
