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

type mockPrometheusAPI struct {
	data prometheusTimelineData
	err  error
}

func (m *mockPrometheusAPI) fetchMetrics(metricName string, start, end time.Time, resolution int) (*prometheusTimelineData, error) {
	if m.err != nil {
		return nil, m.err
	}
	return &m.data, nil
}

func TestSyncer_Init(t *testing.T) {
	mockDB := testlib.NewMockDB()
	mockDB.Init()
	defer mockDB.Close()

	syncer := &syncer{
		PrometheusAPI: &prometheusAPI{},
		DB:            &mockDB,
	}
	syncer.Init()

	// Verify the table was created
	if _, err := mockDB.Get().Model((*PrometheusMetric)(nil)).Exists(); err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
}

func TestSyncer_getSyncWindowStart(t *testing.T) {
	mockDB := testlib.NewMockDB()
	mockDB.Init()
	defer mockDB.Close()

	// Test case: No metrics in the database
	syncer := &syncer{
		PrometheusAPI: &prometheusAPI{},
		DB:            &mockDB,
	}
	syncer.Init()
	start, err := syncer.getSyncWindowStart("test_metric")
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
		"INSERT INTO metrics (name, timestamp) VALUES (?, ?)",
		"test_metric", latestTimestamp,
	); err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	start, err = syncer.getSyncWindowStart("test_metric")
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

	mockPrometheusAPI := &mockPrometheusAPI{
		data: prometheusTimelineData{
			Metrics: []PrometheusMetric{
				{Name: "test_metric", Timestamp: time.Now(), Value: 123.45},
			},
		},
	}

	syncer := &syncer{
		SyncTimeRange:         4 * 7 * 24 * time.Hour, // 4 weeks
		SyncInterval:          24 * time.Hour,
		SyncResolutionSeconds: 12 * 60 * 60, // 12 hours (2 datapoints per day per metric)
		SyncTimeout:           0,
		PrometheusAPI:         mockPrometheusAPI,
		DB:                    &mockDB,
	}
	syncer.Init()

	start := time.Now().Add(-syncer.SyncTimeRange)
	syncer.sync(start, "test_metric")

	// Verify the metrics were inserted
	var metrics []PrometheusMetric
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

	mockPrometheusAPI := &mockPrometheusAPI{
		err: errors.New("failed to fetch metrics"),
	}

	syncer := &syncer{
		SyncTimeRange:         4 * 7 * 24 * time.Hour, // 4 weeks
		SyncInterval:          24 * time.Hour,
		SyncResolutionSeconds: 12 * 60 * 60, // 12 hours (2 datapoints per day per metric)
		SyncTimeout:           0,
		PrometheusAPI:         mockPrometheusAPI,
		DB:                    &mockDB,
	}
	syncer.Init()

	start := time.Now().Add(-syncer.SyncTimeRange)
	syncer.sync(start, "test_metric")

	// Verify no metrics were inserted
	var metrics []PrometheusMetric
	if err := mockDB.Get().Model(&metrics).Select(); err != nil && errors.Is(err, pg.ErrNoRows) {
		t.Fatalf("expected no error, got %v", err)
	}
	if len(metrics) != 0 {
		t.Errorf("expected 0 metrics, got %d", len(metrics))
	}
}
