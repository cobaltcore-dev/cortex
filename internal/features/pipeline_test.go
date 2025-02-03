// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package features

import (
	"errors"
	"testing"

	"github.com/cobaltcore-dev/cortex/internal/db"
	"github.com/cobaltcore-dev/cortex/internal/features/plugins"
)

type mockFeatureExtractor struct {
	initErr    error
	extractErr error
}

func (m *mockFeatureExtractor) Init(db db.DB, opts map[string]any) error {
	return m.initErr
}

func (m *mockFeatureExtractor) Extract() error {
	return m.extractErr
}

func (m *mockFeatureExtractor) GetName() string {
	return "mock_feature_extractor"
}

func TestFeatureExtractorPipeline_Extract(t *testing.T) {
	// Test case: All extractors extract successfully
	pipeline := &FeatureExtractorPipeline{
		extractors: []plugins.FeatureExtractor{
			&mockFeatureExtractor{},
			&mockFeatureExtractor{},
		},
	}

	pipeline.Extract()

	// No errors means the test passed
}

func TestFeatureExtractorPipeline_Extract_Failure(t *testing.T) {
	// Test case: One extractor fails to extract
	pipeline := &FeatureExtractorPipeline{
		extractors: []plugins.FeatureExtractor{
			&mockFeatureExtractor{},
			&mockFeatureExtractor{extractErr: errors.New("extract error")},
		},
	}

	pipeline.Extract()

	// No panic means the test passed
}
