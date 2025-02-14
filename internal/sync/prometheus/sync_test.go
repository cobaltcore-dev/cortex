// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package prometheus

import (
	"errors"
	"testing"
	"time"

	testlibDB "github.com/cobaltcore-dev/cortex/testlib/db"
)

type mockPrometheusAPI[M PrometheusMetric] struct {
	data prometheusTimelineData[M]
	err  error
}

func (api *mockPrometheusAPI[M]) FetchMetrics(
	query string,
	start time.Time,
	end time.Time,
	resolutionSeconds int,
) (*prometheusTimelineData[M], error) {
	// Return the error if set
	if api.err != nil {
		return nil, api.err
	}
	return &api.data, nil
}

func TestSyncer_Init(t *testing.T) {
	testDBManager := testlibDB.NewTestDB(t)
	defer testDBManager.Close()
	testDB := testDBManager.GetDB()

	syncer := &syncer[VROpsVMMetric]{
		MetricName:    "test_metric",
		PrometheusAPI: &mockPrometheusAPI[VROpsVMMetric]{},
		DB:            *testDB,
	}
	syncer.Init()

	// Verify the table was created
	if !testDB.TableExists(&VROpsVMMetric{}) {
		t.Error("expected table to be created")
	}
}

func TestSyncer_getSyncWindowStart(t *testing.T) {
	testDBManager := testlibDB.NewTestDB(t)
	defer testDBManager.Close()
	testDB := testDBManager.GetDB()

	// Test case: No metrics in the database
	syncer := &syncer[VROpsVMMetric]{
		MetricName:    "test_metric",
		PrometheusAPI: &mockPrometheusAPI[VROpsVMMetric]{},
		DB:            *testDB,
	}
	syncer.Init()
	start, err := syncer.getSyncWindowStart()
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	expectedStart := time.Now().Add(-syncer.SyncTimeRange)
	if !start.Before(time.Now()) || !start.After(expectedStart.Add(-time.Minute)) {
		t.Errorf("expected start to be around %v, got %v", expectedStart, start)
	}

	// Test case: Metrics in the database
	latestTimestamp := time.Now().Add(-time.Hour)
	if _, err = testDB.Exec(
		"INSERT INTO vrops_vm_metrics (name, timestamp) VALUES (:metric, :timestamp)",
		map[string]any{"metric": "test_metric", "timestamp": latestTimestamp},
	); err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	start, err = syncer.getSyncWindowStart()
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	// Equal to the second to avoid floating point errors
	startSecondsUnix := start.Unix()
	latestTimestampSecondsUnix := latestTimestamp.Unix()
	if startSecondsUnix != latestTimestampSecondsUnix {
		t.Errorf("expected start to be %v, got %v", latestTimestamp, start)
	}
}

func TestSyncer_sync(t *testing.T) {
	testDBManager := testlibDB.NewTestDB(t)
	defer testDBManager.Close()
	testDB := testDBManager.GetDB()

	mockPrometheusAPI := &mockPrometheusAPI[VROpsVMMetric]{
		data: prometheusTimelineData[VROpsVMMetric]{
			Metrics: []VROpsVMMetric{
				{Name: "test_metric", Timestamp: time.Now(), Value: 123.45},
			},
		},
	}

	syncer := &syncer[VROpsVMMetric]{
		SyncTimeRange:         4 * 7 * 24 * time.Hour, // 4 weeks
		SyncInterval:          24 * time.Hour,
		SyncResolutionSeconds: 12 * 60 * 60, // 12 hours (2 datapoints per day per metric)
		MetricName:            "test_metric",
		PrometheusAPI:         mockPrometheusAPI,
		DB:                    *testDB,
	}
	syncer.Init()

	start := time.Now().Add(-syncer.SyncTimeRange)
	syncer.sync(start)

	// Verify the metrics were inserted
	var metrics []VROpsVMMetric
	if _, err := testDB.Select(&metrics, "SELECT * FROM vrops_vm_metrics"); err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if len(metrics) != 4*7 {
		// Mock returns 1 datapoint each time called
		t.Errorf("expected 4 weeks of datapoints, got %d", len(metrics))
	}
	if metrics[0].Name != "test_metric" {
		t.Errorf("expected metric name to be %s, got %s", "test_metric", metrics[0].Name)
	}
	if metrics[0].Value != 123.45 {
		t.Errorf("expected metric value to be %f, got %f", 123.45, metrics[0].Value)
	}
}

func TestSyncer_sync_Failure(t *testing.T) {
	testDBManager := testlibDB.NewTestDB(t)
	defer testDBManager.Close()
	testDB := testDBManager.GetDB()

	mockPrometheusAPI := &mockPrometheusAPI[VROpsVMMetric]{
		err: errors.New("failed to fetch metrics"),
	}

	syncer := &syncer[VROpsVMMetric]{
		SyncTimeRange:         4 * 7 * 24 * time.Hour, // 4 weeks
		SyncInterval:          24 * time.Hour,
		SyncResolutionSeconds: 12 * 60 * 60, // 12 hours (2 datapoints per day per metric)
		MetricName:            "test_metric",
		PrometheusAPI:         mockPrometheusAPI,
		DB:                    *testDB,
	}
	syncer.Init()

	start := time.Now().Add(-syncer.SyncTimeRange)
	syncer.sync(start)

	// Verify no metrics were inserted
	var metrics []VROpsVMMetric
	if _, err := testDB.Select(&metrics, "SELECT * FROM vrops_vm_metrics"); err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if len(metrics) != 0 {
		t.Errorf("expected 0 metrics, got %d", len(metrics))
	}
}

func TestSyncer_DeleteOldMetrics(t *testing.T) {
	testDBManager := testlibDB.NewTestDB(t)
	defer testDBManager.Close()
	testDB := testDBManager.GetDB()

	mockPrometheusAPI := &mockPrometheusAPI[VROpsVMMetric]{
		data: prometheusTimelineData[VROpsVMMetric]{
			Metrics: []VROpsVMMetric{
				{Name: "test_metric", Timestamp: time.Now(), Value: 123.45},
			},
		},
	}

	syncer := &syncer[VROpsVMMetric]{
		SyncTimeRange:         4 * 7 * 24 * time.Hour, // 4 weeks
		SyncInterval:          24 * time.Hour,
		SyncResolutionSeconds: 12 * 60 * 60, // 12 hours (2 datapoints per day per metric)
		MetricName:            "test_metric",
		PrometheusAPI:         mockPrometheusAPI,
		DB:                    *testDB,
	}
	syncer.Init()

	// Insert old metrics
	oldTimestamp := time.Now().Add(-5 * 7 * 24 * time.Hour) // 5 weeks ago
	if _, err := testDB.Exec(
		"INSERT INTO vrops_vm_metrics (name, timestamp, value) VALUES (:name, :timestamp, :value)",
		map[string]any{"name": "test_metric", "timestamp": oldTimestamp, "value": 123.45},
	); err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	// Insert recent metrics
	recentTimestamp := time.Now().Add(-2 * 7 * 24 * time.Hour) // 2 weeks ago
	if _, err := testDB.Exec(
		"INSERT INTO vrops_vm_metrics (name, timestamp, value) VALUES (:name, :timestamp, :value)",
		map[string]any{"name": "test_metric", "timestamp": recentTimestamp, "value": 123.45},
	); err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	start := time.Now().Add(-syncer.SyncTimeRange)
	syncer.sync(start)

	// Verify old metrics were deleted
	var metrics []VROpsVMMetric
	if _, err := testDB.Select(&metrics, "SELECT name, timestamp, value FROM vrops_vm_metrics"); err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	for _, metric := range metrics {
		if metric.Timestamp.Before(time.Now().Add(-syncer.SyncTimeRange)) {
			t.Errorf("expected old metrics to be deleted, but found metric with timestamp %v", metric.Timestamp)
		}
	}
}
