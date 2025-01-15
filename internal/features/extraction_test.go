package features

import (
	"errors"
	"testing"
)

type mockFeatureExtractor struct {
	initErr    error
	extractErr error
}

func (m *mockFeatureExtractor) Init() error {
	return m.initErr
}

func (m *mockFeatureExtractor) Extract() error {
	return m.extractErr
}

func TestFeatureExtractorPipeline_Init(t *testing.T) {
	// Test case: All extractors initialize successfully
	pipeline := &featureExtractorPipeline{
		FeatureExtractors: []FeatureExtractor{
			&mockFeatureExtractor{},
			&mockFeatureExtractor{},
		},
	}

	// Call the function to test
	pipeline.Init()

	// No panic means the test passed
}

func TestFeatureExtractorPipeline_Init_Failure(t *testing.T) {
	// Test case: One extractor fails to initialize
	pipeline := &featureExtractorPipeline{
		FeatureExtractors: []FeatureExtractor{
			&mockFeatureExtractor{},
			&mockFeatureExtractor{initErr: errors.New("init error")},
		},
	}

	defer func() {
		if r := recover(); r == nil {
			t.Errorf("Expected panic, got none")
		}
	}()

	// Call the function to test
	pipeline.Init()
}

func TestFeatureExtractorPipeline_Extract(t *testing.T) {
	// Test case: All extractors extract successfully
	pipeline := &featureExtractorPipeline{
		FeatureExtractors: []FeatureExtractor{
			&mockFeatureExtractor{},
			&mockFeatureExtractor{},
		},
	}

	// Call the function to test
	pipeline.Extract()

	// No errors means the test passed
}

func TestFeatureExtractorPipeline_Extract_Failure(t *testing.T) {
	// Test case: One extractor fails to extract
	pipeline := &featureExtractorPipeline{
		FeatureExtractors: []FeatureExtractor{
			&mockFeatureExtractor{},
			&mockFeatureExtractor{extractErr: errors.New("extract error")},
		},
	}
	// Call the function to test
	pipeline.Extract()

	// No panic means the test passed
}
