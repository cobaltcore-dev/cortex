// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package extractor

import (
	"errors"
	"testing"

	"github.com/cobaltcore-dev/cortex/api/v1alpha1"
	"github.com/cobaltcore-dev/cortex/internal/knowledge/db"
	"github.com/cobaltcore-dev/cortex/internal/knowledge/extractor/plugins"
	"github.com/prometheus/client_golang/prometheus"
	dto "github.com/prometheus/client_model/go"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// Mock feature extractor for testing
type mockFeatureExtractor struct {
	initError    error
	extractError error
	features     []plugins.Feature
}

func (m *mockFeatureExtractor) Init(datasourceDB *db.DB, client client.Client, spec v1alpha1.KnowledgeSpec) error {
	return m.initError
}

func (m *mockFeatureExtractor) Extract(_ []*v1alpha1.Datasource, _ []*v1alpha1.Knowledge) ([]plugins.Feature, error) {
	if m.extractError != nil {
		return nil, m.extractError
	}
	return m.features, nil
}

func TestNewMonitor(t *testing.T) {
	monitor := NewMonitor()

	if monitor.stepRunTimer == nil {
		t.Error("stepRunTimer should not be nil")
	}
	if monitor.stepFeatureCounter == nil {
		t.Error("stepFeatureCounter should not be nil")
	}
	if monitor.stepSkipCounter == nil {
		t.Error("stepSkipCounter should not be nil")
	}
}

func TestMonitor_DescribeAndCollect(t *testing.T) {
	monitor := NewMonitor()

	// Test Describe
	descCh := make(chan *prometheus.Desc, 10)
	go func() {
		monitor.Describe(descCh)
		close(descCh)
	}()

	var descriptions []*prometheus.Desc
	for desc := range descCh {
		descriptions = append(descriptions, desc)
	}

	if len(descriptions) == 0 {
		t.Error("Expected at least one metric description")
	}

	// Test Collect
	metricCh := make(chan prometheus.Metric, 10)
	go func() {
		monitor.Collect(metricCh)
		close(metricCh)
	}()

	var metrics []prometheus.Metric
	for metric := range metricCh {
		metrics = append(metrics, metric)
	}

	if len(metrics) != 0 {
		t.Error("Expected no metrics collected since no metrics have been recorded yet")
	}
}

func TestMonitorFeatureExtractor_Init(t *testing.T) {
	monitor := NewMonitor()
	mockExtractor := &mockFeatureExtractor{}

	wrappedExtractor := monitorFeatureExtractor("test-extractor", mockExtractor, monitor)

	// Test successful init
	err := wrappedExtractor.Init(nil, nil, v1alpha1.KnowledgeSpec{})
	if err != nil {
		t.Errorf("Expected no error, got %v", err)
	}

	// Test init error
	expectedError := errors.New("init failed")
	mockExtractor.initError = expectedError
	wrappedExtractor = monitorFeatureExtractor("test-extractor", mockExtractor, monitor)

	err = wrappedExtractor.Init(nil, nil, v1alpha1.KnowledgeSpec{})
	if !errors.Is(err, expectedError) {
		t.Errorf("Expected error %v, got %v", expectedError, err)
	}
}

func TestMonitorFeatureExtractor_Extract_Success(t *testing.T) {
	monitor := NewMonitor()
	expectedFeatures := []plugins.Feature{
		"feature1",
		"feature2",
	}
	mockExtractor := &mockFeatureExtractor{
		features: expectedFeatures,
	}

	wrappedExtractor := monitorFeatureExtractor("test-extractor", mockExtractor, monitor)

	features, err := wrappedExtractor.Extract([]*v1alpha1.Datasource{}, []*v1alpha1.Knowledge{})
	if err != nil {
		t.Errorf("Expected no error, got %v", err)
	}

	if len(features) != len(expectedFeatures) {
		t.Errorf("Expected %d features, got %d", len(expectedFeatures), len(features))
	}

	for i, feature := range features {
		if feature != expectedFeatures[i] {
			t.Errorf("Expected feature %v, got %v", expectedFeatures[i], feature)
		}
	}

	// Verify that the feature count was recorded
	if wrappedExtractor.featureCounter != nil {
		metric := &dto.Metric{}
		if err := wrappedExtractor.featureCounter.Write(metric); err != nil {
			t.Errorf("Failed to write metric: %v", err)
		}
		if metric.GetGauge().GetValue() != float64(len(expectedFeatures)) {
			t.Errorf("Expected feature count %d, got %f", len(expectedFeatures), metric.GetGauge().GetValue())
		}
	}
}

func TestMonitorFeatureExtractor_Extract_Error(t *testing.T) {
	monitor := NewMonitor()
	expectedError := errors.New("extraction failed")
	mockExtractor := &mockFeatureExtractor{
		extractError: expectedError,
	}

	wrappedExtractor := monitorFeatureExtractor("test-extractor", mockExtractor, monitor)

	features, err := wrappedExtractor.Extract([]*v1alpha1.Datasource{}, []*v1alpha1.Knowledge{})
	if !errors.Is(err, expectedError) {
		t.Errorf("Expected error %v, got %v", expectedError, err)
	}
	if features != nil {
		t.Error("Expected nil features on error")
	}
}

func TestMonitorFeatureExtractor_Extract_EmptyFeatures(t *testing.T) {
	monitor := NewMonitor()
	mockExtractor := &mockFeatureExtractor{
		features: []plugins.Feature{}, // Empty features
	}

	wrappedExtractor := monitorFeatureExtractor("test-extractor", mockExtractor, monitor)

	features, err := wrappedExtractor.Extract([]*v1alpha1.Datasource{}, []*v1alpha1.Knowledge{})
	if err != nil {
		t.Errorf("Expected no error, got %v", err)
	}

	if len(features) != 0 {
		t.Errorf("Expected 0 features, got %d", len(features))
	}

	// Verify that the feature count was recorded as 0
	if wrappedExtractor.featureCounter != nil {
		metric := &dto.Metric{}
		if err := wrappedExtractor.featureCounter.Write(metric); err != nil {
			t.Errorf("Failed to write metric: %v", err)
		}
		if metric.GetGauge().GetValue() != 0 {
			t.Errorf("Expected feature count 0, got %f", metric.GetGauge().GetValue())
		}
	}
}

func TestMonitorFeatureExtractor_NilMonitor(t *testing.T) {
	// Test with a monitor that has nil metrics (edge case)
	monitor := Monitor{}
	mockExtractor := &mockFeatureExtractor{
		features: []plugins.Feature{"test-feature"},
	}

	wrappedExtractor := monitorFeatureExtractor("test-extractor", mockExtractor, monitor)

	// Should not panic even with nil metrics
	features, err := wrappedExtractor.Extract([]*v1alpha1.Datasource{}, []*v1alpha1.Knowledge{})
	if err != nil {
		t.Errorf("Expected no error, got %v", err)
	}
	if len(features) != 1 {
		t.Errorf("Expected 1 feature, got %d", len(features))
	}
}

func TestMonitorFeatureExtractor_Label(t *testing.T) {
	monitor := NewMonitor()
	mockExtractor := &mockFeatureExtractor{}

	expectedLabel := "test-extractor-label"
	wrappedExtractor := monitorFeatureExtractor(expectedLabel, mockExtractor, monitor)

	if wrappedExtractor.label != expectedLabel {
		t.Errorf("Expected label %s, got %s", expectedLabel, wrappedExtractor.label)
	}
}

func TestMonitorFeatureExtractor_MultipleExtractions(t *testing.T) {
	monitor := NewMonitor()
	mockExtractor := &mockFeatureExtractor{
		features: []plugins.Feature{
			"feature1",
			"feature2",
		},
	}

	wrappedExtractor := monitorFeatureExtractor("test-extractor", mockExtractor, monitor)

	// First extraction
	features1, err := wrappedExtractor.Extract([]*v1alpha1.Datasource{}, []*v1alpha1.Knowledge{})
	if err != nil {
		t.Errorf("First extraction failed: %v", err)
	}
	if len(features1) != 2 {
		t.Errorf("Expected 2 features in first extraction, got %d", len(features1))
	}

	// Change the mock to return different features
	mockExtractor.features = []plugins.Feature{
		"feature3",
	}

	// Second extraction
	features2, err := wrappedExtractor.Extract([]*v1alpha1.Datasource{}, []*v1alpha1.Knowledge{})
	if err != nil {
		t.Errorf("Second extraction failed: %v", err)
	}
	if len(features2) != 1 {
		t.Errorf("Expected 1 feature in second extraction, got %d", len(features2))
	}

	// Verify that the feature count was updated
	if wrappedExtractor.featureCounter != nil {
		metric := &dto.Metric{}
		if err := wrappedExtractor.featureCounter.Write(metric); err != nil {
			t.Errorf("Failed to write metric: %v", err)
		}
		// Should reflect the latest extraction count
		if metric.GetGauge().GetValue() != 1 {
			t.Errorf("Expected latest feature count 1, got %f", metric.GetGauge().GetValue())
		}
	}
}

func TestMonitorFeatureExtractor_DifferentExtractorNames(t *testing.T) {
	monitor := NewMonitor()
	mockExtractor1 := &mockFeatureExtractor{
		features: []plugins.Feature{"feature1"},
	}
	mockExtractor2 := &mockFeatureExtractor{
		features: []plugins.Feature{
			"feature2",
			"feature3",
		},
	}

	wrappedExtractor1 := monitorFeatureExtractor("extractor-1", mockExtractor1, monitor)
	wrappedExtractor2 := monitorFeatureExtractor("extractor-2", mockExtractor2, monitor)

	// Extract from both
	_, err1 := wrappedExtractor1.Extract([]*v1alpha1.Datasource{}, []*v1alpha1.Knowledge{})
	if err1 != nil {
		t.Errorf("Extractor 1 failed: %v", err1)
	}

	_, err2 := wrappedExtractor2.Extract([]*v1alpha1.Datasource{}, []*v1alpha1.Knowledge{})
	if err2 != nil {
		t.Errorf("Extractor 2 failed: %v", err2)
	}

	// Verify different counters are used (they should have different labels)
	if wrappedExtractor1.featureCounter != nil && wrappedExtractor2.featureCounter != nil {
		metric1 := &dto.Metric{}
		metric2 := &dto.Metric{}

		if err := wrappedExtractor1.featureCounter.Write(metric1); err != nil {
			t.Errorf("Failed to write metric1: %v", err)
		}
		if err := wrappedExtractor2.featureCounter.Write(metric2); err != nil {
			t.Errorf("Failed to write metric2: %v", err)
		}

		if metric1.GetGauge().GetValue() != 1 {
			t.Errorf("Expected extractor1 feature count 1, got %f", metric1.GetGauge().GetValue())
		}
		if metric2.GetGauge().GetValue() != 2 {
			t.Errorf("Expected extractor2 feature count 2, got %f", metric2.GetGauge().GetValue())
		}
	}
}
