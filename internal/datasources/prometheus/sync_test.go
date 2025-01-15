// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package prometheus

import (
	"testing"
	"time"

	"github.com/cobaltcore-dev/cortex/testlib"
)

func TestGetSyncWindowStart(t *testing.T) {
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
	expectedStart := time.Now().Add(-syncTimeRange)
	if !start.Before(time.Now()) || !start.After(expectedStart.Add(-time.Minute)) {
		t.Errorf("expected start to be around %v, got %v", expectedStart, start)
	}

	// Test case: Metrics in the database
	latestTimestamp := time.Now().Add(-time.Hour)
	_, err = mockDB.Get().Exec(
		"INSERT INTO metrics (name, timestamp) VALUES (?, ?)",
		"test_metric", latestTimestamp,
	)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	start, err = syncer.getSyncWindowStart("test_metric")
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if !start.Equal(latestTimestamp) {
		t.Errorf("expected start to be %v, got %v", latestTimestamp, start)
	}
}
