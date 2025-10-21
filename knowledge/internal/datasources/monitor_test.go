// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package datasources

import (
	"strings"
	"testing"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"
)

func TestNewSyncMonitor(t *testing.T) {
	monitor := NewSyncMonitor()

	// Test that all metrics are properly initialized
	if monitor.PipelineRunTimer == nil {
		t.Error("PipelineRunTimer should not be nil")
	}
	if monitor.PipelineObjectsGauge == nil {
		t.Error("PipelineObjectsGauge should not be nil")
	}
	if monitor.PipelineRequestTimer == nil {
		t.Error("PipelineRequestTimer should not be nil")
	}
	if monitor.PipelineRequestProcessedCounter == nil {
		t.Error("PipelineRequestProcessedCounter should not be nil")
	}
}

func TestMonitorDescribe(t *testing.T) {
	monitor := NewSyncMonitor()
	ch := make(chan *prometheus.Desc, 10)

	go func() {
		monitor.Describe(ch)
		close(ch)
	}()

	var descs []*prometheus.Desc
	for desc := range ch {
		descs = append(descs, desc)
	}

	// We expect at least 4 descriptors (one for each metric)
	if len(descs) < 4 {
		t.Errorf("Expected at least 4 descriptors, got %d", len(descs))
	}

	// Check that we have descriptors for all our metrics
	var foundMetrics []string
	for _, desc := range descs {
		if strings.Contains(desc.String(), "cortex_sync_run_duration_seconds") {
			foundMetrics = append(foundMetrics, "run_timer")
		}
		if strings.Contains(desc.String(), "cortex_sync_objects") {
			foundMetrics = append(foundMetrics, "objects_gauge")
		}
		if strings.Contains(desc.String(), "cortex_sync_request_duration_seconds") {
			foundMetrics = append(foundMetrics, "request_timer")
		}
		if strings.Contains(desc.String(), "cortex_sync_request_processed_total") {
			foundMetrics = append(foundMetrics, "request_counter")
		}
	}

	expectedMetrics := []string{"run_timer", "objects_gauge", "request_timer", "request_counter"}
	for _, expected := range expectedMetrics {
		found := false
		for _, actual := range foundMetrics {
			if actual == expected {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("Expected to find metric %s in descriptors", expected)
		}
	}
}

func TestMonitorCollect(t *testing.T) {
	monitor := NewSyncMonitor()
	ch := make(chan prometheus.Metric, 20)

	go func() {
		monitor.Collect(ch)
		close(ch)
	}()

	var metrics []prometheus.Metric
	for metric := range ch {
		metrics = append(metrics, metric)
	}

	// We should have metrics from all our collectors
	if len(metrics) == 0 {
		t.Error("Expected to collect some metrics")
	}
}

func TestPipelineRunTimerMetric(t *testing.T) {
	monitor := NewSyncMonitor()

	// Test that the histogram has the correct name and help
	expected := `
		# HELP cortex_sync_run_duration_seconds Duration of sync run
		# TYPE cortex_sync_run_duration_seconds histogram
	`

	if err := testutil.CollectAndCompare(monitor.PipelineRunTimer, strings.NewReader(expected)); err != nil {
		t.Errorf("Unexpected metric output: %v", err)
	}

	// Test observing a value
	monitor.PipelineRunTimer.WithLabelValues("test-datasource").Observe(1.5)

	// Check that the observation was recorded
	expectedWithValue := `
		# HELP cortex_sync_run_duration_seconds Duration of sync run
		# TYPE cortex_sync_run_duration_seconds histogram
		cortex_sync_run_duration_seconds_bucket{datasource="test-datasource",le="0.001"} 0
		cortex_sync_run_duration_seconds_bucket{datasource="test-datasource",le="0.002"} 0
		cortex_sync_run_duration_seconds_bucket{datasource="test-datasource",le="0.004"} 0
		cortex_sync_run_duration_seconds_bucket{datasource="test-datasource",le="0.008"} 0
		cortex_sync_run_duration_seconds_bucket{datasource="test-datasource",le="0.016"} 0
		cortex_sync_run_duration_seconds_bucket{datasource="test-datasource",le="0.032"} 0
		cortex_sync_run_duration_seconds_bucket{datasource="test-datasource",le="0.064"} 0
		cortex_sync_run_duration_seconds_bucket{datasource="test-datasource",le="0.128"} 0
		cortex_sync_run_duration_seconds_bucket{datasource="test-datasource",le="0.256"} 0
		cortex_sync_run_duration_seconds_bucket{datasource="test-datasource",le="0.512"} 0
		cortex_sync_run_duration_seconds_bucket{datasource="test-datasource",le="1.024"} 0
		cortex_sync_run_duration_seconds_bucket{datasource="test-datasource",le="2.048"} 1
		cortex_sync_run_duration_seconds_bucket{datasource="test-datasource",le="4.096"} 1
		cortex_sync_run_duration_seconds_bucket{datasource="test-datasource",le="8.192"} 1
		cortex_sync_run_duration_seconds_bucket{datasource="test-datasource",le="16.384"} 1
		cortex_sync_run_duration_seconds_bucket{datasource="test-datasource",le="32.768"} 1
		cortex_sync_run_duration_seconds_bucket{datasource="test-datasource",le="65.536"} 1
		cortex_sync_run_duration_seconds_bucket{datasource="test-datasource",le="131.072"} 1
		cortex_sync_run_duration_seconds_bucket{datasource="test-datasource",le="262.144"} 1
		cortex_sync_run_duration_seconds_bucket{datasource="test-datasource",le="524.288"} 1
		cortex_sync_run_duration_seconds_bucket{datasource="test-datasource",le="1048.576"} 1
		cortex_sync_run_duration_seconds_bucket{datasource="test-datasource",le="+Inf"} 1
		cortex_sync_run_duration_seconds_sum{datasource="test-datasource"} 1.5
		cortex_sync_run_duration_seconds_count{datasource="test-datasource"} 1
	`

	if err := testutil.CollectAndCompare(monitor.PipelineRunTimer, strings.NewReader(expectedWithValue)); err != nil {
		t.Errorf("Unexpected metric output after observation: %v", err)
	}
}

func TestPipelineObjectsGaugeMetric(t *testing.T) {
	monitor := NewSyncMonitor()

	// Test that the gauge has the correct name and help
	expected := `
		# HELP cortex_sync_objects Number of objects synced
		# TYPE cortex_sync_objects gauge
	`

	if err := testutil.CollectAndCompare(monitor.PipelineObjectsGauge, strings.NewReader(expected)); err != nil {
		t.Errorf("Unexpected metric output: %v", err)
	}

	// Test setting a value
	monitor.PipelineObjectsGauge.WithLabelValues("test-datasource").Set(42)

	expectedWithValue := `
		# HELP cortex_sync_objects Number of objects synced
		# TYPE cortex_sync_objects gauge
		cortex_sync_objects{datasource="test-datasource"} 42
	`

	if err := testutil.CollectAndCompare(monitor.PipelineObjectsGauge, strings.NewReader(expectedWithValue)); err != nil {
		t.Errorf("Unexpected metric output after setting value: %v", err)
	}
}

func TestPipelineRequestTimerMetric(t *testing.T) {
	monitor := NewSyncMonitor()

	// Test that the histogram has the correct name and help
	expected := `
		# HELP cortex_sync_request_duration_seconds Duration of sync request
		# TYPE cortex_sync_request_duration_seconds histogram
	`

	if err := testutil.CollectAndCompare(monitor.PipelineRequestTimer, strings.NewReader(expected)); err != nil {
		t.Errorf("Unexpected metric output: %v", err)
	}

	// Test observing a value
	monitor.PipelineRequestTimer.WithLabelValues("test-datasource").Observe(0.5)

	// Check that the observation was recorded (using default buckets)
	expectedWithValue := `
		# HELP cortex_sync_request_duration_seconds Duration of sync request
		# TYPE cortex_sync_request_duration_seconds histogram
		cortex_sync_request_duration_seconds_bucket{datasource="test-datasource",le="0.005"} 0
		cortex_sync_request_duration_seconds_bucket{datasource="test-datasource",le="0.01"} 0
		cortex_sync_request_duration_seconds_bucket{datasource="test-datasource",le="0.025"} 0
		cortex_sync_request_duration_seconds_bucket{datasource="test-datasource",le="0.05"} 0
		cortex_sync_request_duration_seconds_bucket{datasource="test-datasource",le="0.1"} 0
		cortex_sync_request_duration_seconds_bucket{datasource="test-datasource",le="0.25"} 0
		cortex_sync_request_duration_seconds_bucket{datasource="test-datasource",le="0.5"} 1
		cortex_sync_request_duration_seconds_bucket{datasource="test-datasource",le="1"} 1
		cortex_sync_request_duration_seconds_bucket{datasource="test-datasource",le="2.5"} 1
		cortex_sync_request_duration_seconds_bucket{datasource="test-datasource",le="5"} 1
		cortex_sync_request_duration_seconds_bucket{datasource="test-datasource",le="10"} 1
		cortex_sync_request_duration_seconds_bucket{datasource="test-datasource",le="+Inf"} 1
		cortex_sync_request_duration_seconds_sum{datasource="test-datasource"} 0.5
		cortex_sync_request_duration_seconds_count{datasource="test-datasource"} 1
	`

	if err := testutil.CollectAndCompare(monitor.PipelineRequestTimer, strings.NewReader(expectedWithValue)); err != nil {
		t.Errorf("Unexpected metric output after observation: %v", err)
	}
}

func TestPipelineRequestProcessedCounterMetric(t *testing.T) {
	monitor := NewSyncMonitor()

	// Test that the counter has the correct name and help
	expected := `
		# HELP cortex_sync_request_processed_total Number of processed sync requests
		# TYPE cortex_sync_request_processed_total counter
	`

	if err := testutil.CollectAndCompare(monitor.PipelineRequestProcessedCounter, strings.NewReader(expected)); err != nil {
		t.Errorf("Unexpected metric output: %v", err)
	}

	// Test incrementing the counter
	monitor.PipelineRequestProcessedCounter.WithLabelValues("test-datasource").Inc()

	expectedWithValue := `
		# HELP cortex_sync_request_processed_total Number of processed sync requests
		# TYPE cortex_sync_request_processed_total counter
		cortex_sync_request_processed_total{datasource="test-datasource"} 1
	`

	if err := testutil.CollectAndCompare(monitor.PipelineRequestProcessedCounter, strings.NewReader(expectedWithValue)); err != nil {
		t.Errorf("Unexpected metric output after increment: %v", err)
	}
}

func TestMultipleDatasourceLabels(t *testing.T) {
	monitor := NewSyncMonitor()

	// Test with multiple datasource labels
	monitor.PipelineObjectsGauge.WithLabelValues("prometheus").Set(10)
	monitor.PipelineObjectsGauge.WithLabelValues("openstack").Set(20)
	monitor.PipelineRequestProcessedCounter.WithLabelValues("prometheus").Inc()
	monitor.PipelineRequestProcessedCounter.WithLabelValues("openstack").Add(5)

	expectedGauge := `
		# HELP cortex_sync_objects Number of objects synced
		# TYPE cortex_sync_objects gauge
		cortex_sync_objects{datasource="openstack"} 20
		cortex_sync_objects{datasource="prometheus"} 10
	`

	if err := testutil.CollectAndCompare(monitor.PipelineObjectsGauge, strings.NewReader(expectedGauge)); err != nil {
		t.Errorf("Unexpected gauge output with multiple labels: %v", err)
	}

	expectedCounter := `
		# HELP cortex_sync_request_processed_total Number of processed sync requests
		# TYPE cortex_sync_request_processed_total counter
		cortex_sync_request_processed_total{datasource="openstack"} 5
		cortex_sync_request_processed_total{datasource="prometheus"} 1
	`

	if err := testutil.CollectAndCompare(monitor.PipelineRequestProcessedCounter, strings.NewReader(expectedCounter)); err != nil {
		t.Errorf("Unexpected counter output with multiple labels: %v", err)
	}
}

func TestMonitorMetricNames(t *testing.T) {
	monitor := NewSyncMonitor()

	// Test that all metrics have the expected names by checking metric families
	registry := prometheus.NewRegistry()
	registry.MustRegister(&monitor)

	metricFamilies, err := registry.Gather()
	if err != nil {
		t.Fatalf("Failed to gather metrics: %v", err)
	}

	expectedNames := map[string]bool{
		"cortex_sync_run_duration_seconds":     false,
		"cortex_sync_objects":                  false,
		"cortex_sync_request_duration_seconds": false,
		"cortex_sync_request_processed_total":  false,
	}

	for _, mf := range metricFamilies {
		name := mf.GetName()
		if _, exists := expectedNames[name]; exists {
			expectedNames[name] = true
		}
	}

	for name, found := range expectedNames {
		if !found {
			t.Errorf("Expected metric %s not found in registered metrics", name)
		}
	}
}

func TestMonitorMetricTypes(t *testing.T) {
	monitor := NewSyncMonitor()

	// Test that metrics have the correct types
	registry := prometheus.NewRegistry()
	registry.MustRegister(&monitor)

	metricFamilies, err := registry.Gather()
	if err != nil {
		t.Fatalf("Failed to gather metrics: %v", err)
	}

	expectedTypes := map[string]string{
		"cortex_sync_run_duration_seconds":     "HISTOGRAM",
		"cortex_sync_objects":                  "GAUGE",
		"cortex_sync_request_duration_seconds": "HISTOGRAM",
		"cortex_sync_request_processed_total":  "COUNTER",
	}

	for _, mf := range metricFamilies {
		name := mf.GetName()
		if expectedType, exists := expectedTypes[name]; exists {
			actualType := mf.GetType().String()
			if actualType != expectedType {
				t.Errorf("Metric %s has type %s, expected %s", name, actualType, expectedType)
			}
		}
	}
}

func TestMonitorBucketConfiguration(t *testing.T) {
	monitor := NewSyncMonitor()

	// Test that PipelineRunTimer uses exponential buckets
	// This is harder to test directly, but we can observe values and check bucket distribution
	monitor.PipelineRunTimer.WithLabelValues("test").Observe(0.0005) // Should be in first bucket
	monitor.PipelineRunTimer.WithLabelValues("test").Observe(2.0)    // Should be in middle bucket
	monitor.PipelineRunTimer.WithLabelValues("test").Observe(1000.0) // Should be in last bucket

	// Test that PipelineRequestTimer uses default buckets
	monitor.PipelineRequestTimer.WithLabelValues("test").Observe(0.001) // Should be in first bucket
	monitor.PipelineRequestTimer.WithLabelValues("test").Observe(1.0)   // Should be in middle bucket
	monitor.PipelineRequestTimer.WithLabelValues("test").Observe(15.0)  // Should be in last bucket

	// We can't easily assert the exact bucket configuration without access to internal data,
	// but we can verify that observations are recorded correctly by checking the metrics output
	registry := prometheus.NewRegistry()
	registry.MustRegister(&monitor)

	metricFamilies, err := registry.Gather()
	if err != nil {
		t.Fatalf("Failed to gather metrics: %v", err)
	}

	// Find the run timer metric family
	for _, mf := range metricFamilies {
		if mf.GetName() == "cortex_sync_run_duration_seconds" {
			// Should have multiple buckets due to exponential bucket configuration
			metrics := mf.GetMetric()
			if len(metrics) == 0 {
				t.Error("Expected to find metrics for run timer")
				continue
			}

			histogram := metrics[0].GetHistogram()
			if histogram == nil {
				t.Error("Expected histogram metric")
				continue
			}

			buckets := histogram.GetBucket()
			// Exponential buckets should create 21 buckets
			if len(buckets) < 20 { // Allow some flexibility
				t.Errorf("Expected around 21 buckets for exponential configuration, got %d", len(buckets))
			}
		}
	}
}

func TestMonitorRegistration(t *testing.T) {
	monitor := NewSyncMonitor()
	registry := prometheus.NewRegistry()

	// Test that the monitor can be registered without errors
	err := registry.Register(&monitor)
	if err != nil {
		t.Errorf("Failed to register monitor: %v", err)
	}

	// Test that registering the same monitor twice fails
	err = registry.Register(&monitor)
	if err == nil {
		t.Error("Expected error when registering monitor twice")
	}
}

func TestMonitorLabels(t *testing.T) {
	monitor := NewSyncMonitor()

	// Test that all metrics accept the datasource label
	datasources := []string{"prometheus", "openstack-nova", "openstack-cinder", "custom-ds"}

	for _, ds := range datasources {
		monitor.PipelineRunTimer.WithLabelValues(ds).Observe(1.0)
		monitor.PipelineObjectsGauge.WithLabelValues(ds).Set(100)
		monitor.PipelineRequestTimer.WithLabelValues(ds).Observe(0.5)
		monitor.PipelineRequestProcessedCounter.WithLabelValues(ds).Inc()
	}

	// Verify all metrics were created with the correct labels
	registry := prometheus.NewRegistry()
	registry.MustRegister(&monitor)

	metricFamilies, err := registry.Gather()
	if err != nil {
		t.Fatalf("Failed to gather metrics: %v", err)
	}

	for _, mf := range metricFamilies {
		metrics := mf.GetMetric()
		if len(metrics) != len(datasources) {
			t.Errorf("Metric %s should have %d entries (one per datasource), got %d",
				mf.GetName(), len(datasources), len(metrics))
		}

		// Check that each metric has the datasource label
		for _, metric := range metrics {
			labels := metric.GetLabel()
			found := false
			for _, label := range labels {
				if label.GetName() == "datasource" {
					found = true
					break
				}
			}
			if !found {
				t.Errorf("Metric %s missing datasource label", mf.GetName())
			}
		}
	}
}
