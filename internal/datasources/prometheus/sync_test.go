// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package prometheus

import (
	"testing"
	"time"

	"github.com/cobaltcore-dev/cortex/internal/db"
	"github.com/cobaltcore-dev/cortex/testlib"
)

func TestMain(m *testing.M) {
	testlib.WithMockDB(m, 5)
}

func TestGetSyncWindowStart(t *testing.T) {
	// Test case: No metrics in the database
	start, err := getSyncWindowStart("test_metric")
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	expectedStart := time.Now().Add(-syncTimeRange)
	if !start.Before(time.Now()) || !start.After(expectedStart.Add(-time.Minute)) {
		t.Errorf("expected start to be around %v, got %v", expectedStart, start)
	}

	// Test case: Metrics in the database
	latestTimestamp := time.Now().Add(-time.Hour)
	_, err = db.Get().Exec("INSERT INTO metrics (name, timestamp) VALUES (?, ?)", "test_metric", latestTimestamp)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	start, err = getSyncWindowStart("test_metric")
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if !start.Equal(latestTimestamp) {
		t.Errorf("expected start to be %v, got %v", latestTimestamp, start)
	}
}

func TestInit(t *testing.T) {
	// Call the function to test
	Init()

	// Verify the table was created
	exists, err := db.Get().Model((*PrometheusMetric)(nil)).Exists()
	if err != nil {
		t.Fatalf("Expected no error, got %v", err)
	}
	if !exists {
		t.Errorf("Expected table for PrometheusMetric to exist")
	}
}
