// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package prometheus

import (
	"errors"
	"testing"
	"time"

	"github.com/cobaltcore-dev/cortex/testlib"
	"github.com/go-pg/pg/v10"
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
	mockDB := testlib.NewMockDB()
	mockDB.Init()
	defer mockDB.Close()

	syncer := &syncer[*VROpsVMMetric]{
		MetricName:    "test_metric",
		PrometheusAPI: &mockPrometheusAPI[*VROpsVMMetric]{},
		DB:            &mockDB,
	}
	syncer.Init()

	// Verify the table was created
	if _, err := mockDB.Get().Model((*VROpsVMMetric)(nil)).Exists(); err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
}

func TestSyncer_getSyncWindowStart(t *testing.T) {
	mockDB := testlib.NewMockDB()
	mockDB.Init()
	defer mockDB.Close()

	// Test case: No metrics in the database
	syncer := &syncer[*VROpsVMMetric]{
		MetricName:    "test_metric",
		PrometheusAPI: &mockPrometheusAPI[*VROpsVMMetric]{},
		DB:            &mockDB,
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
	if _, err = mockDB.Get().Exec(
		"INSERT INTO vrops_vm_metrics (name, timestamp) VALUES (?, ?)",
		"test_metric", latestTimestamp,
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
	mockDB := testlib.NewMockDB()
	mockDB.Init()
	defer mockDB.Close()

	mockPrometheusAPI := &mockPrometheusAPI[*VROpsVMMetric]{
		data: prometheusTimelineData[*VROpsVMMetric]{
			Metrics: []*VROpsVMMetric{
				{Name: "test_metric", Timestamp: time.Now(), Value: 123.45},
			},
		},
	}

	syncer := &syncer[*VROpsVMMetric]{
		SyncTimeRange:         4 * 7 * 24 * time.Hour, // 4 weeks
		SyncInterval:          24 * time.Hour,
		SyncResolutionSeconds: 12 * 60 * 60, // 12 hours (2 datapoints per day per metric)
		MetricName:            "test_metric",
		PrometheusAPI:         mockPrometheusAPI,
		DB:                    &mockDB,
	}
	syncer.Init()

	start := time.Now().Add(-syncer.SyncTimeRange)
	syncer.sync(start)

	// Verify the metrics were inserted
	var metrics []VROpsVMMetric
	if err := mockDB.Get().Model(&metrics).Select(); err != nil {
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
	mockDB := testlib.NewMockDB()
	mockDB.Init()
	defer mockDB.Close()

	mockPrometheusAPI := &mockPrometheusAPI[*VROpsVMMetric]{
		err: errors.New("failed to fetch metrics"),
	}

	syncer := &syncer[*VROpsVMMetric]{
		SyncTimeRange:         4 * 7 * 24 * time.Hour, // 4 weeks
		SyncInterval:          24 * time.Hour,
		SyncResolutionSeconds: 12 * 60 * 60, // 12 hours (2 datapoints per day per metric)
		MetricName:            "test_metric",
		PrometheusAPI:         mockPrometheusAPI,
		DB:                    &mockDB,
	}
	syncer.Init()

	start := time.Now().Add(-syncer.SyncTimeRange)
	syncer.sync(start)

	// Verify no metrics were inserted
	var metrics []PrometheusMetric
	if err := mockDB.Get().Model(&metrics).Select(); err != nil && errors.Is(err, pg.ErrNoRows) {
		t.Fatalf("expected no error, got %v", err)
	}
	if len(metrics) != 0 {
		t.Errorf("expected 0 metrics, got %d", len(metrics))
	}
}
