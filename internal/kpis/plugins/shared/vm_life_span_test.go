// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package shared

import (
	"testing"

	"github.com/cobaltcore-dev/cortex/internal/conf"
	"github.com/cobaltcore-dev/cortex/internal/db"
	"github.com/cobaltcore-dev/cortex/internal/extractor/plugins/shared"
	testlibDB "github.com/cobaltcore-dev/cortex/testlib/db"
	"github.com/prometheus/client_golang/prometheus"
)

func TestVMLifeSpanKPI_Init(t *testing.T) {
	dbEnv := testlibDB.SetupDBEnv(t)
	testDB := db.DB{DbMap: dbEnv.DbMap}
	defer testDB.Close()
	defer dbEnv.Close()

	kpi := &VMLifeSpanKPI{}
	if err := kpi.Init(testDB, conf.NewRawOpts("{}")); err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
}

func TestVMLifeSpanKPI_Collect(t *testing.T) {
	dbEnv := testlibDB.SetupDBEnv(t)
	testDB := db.DB{DbMap: dbEnv.DbMap}
	defer testDB.Close()
	defer dbEnv.Close()

	// Create dependency tables
	if err := testDB.CreateTable(
		testDB.AddTable(shared.VMLifeSpanHistogramBucket{}),
	); err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	// Insert mock data into the table
	_, err := testDB.Exec(`
        INSERT INTO feature_vm_life_span_histogram_bucket (
            flavor_name, bucket, value, count, sum
        )
        VALUES
		    ('small', 60, 100, 10, 600),
		    ('medium', 120, 200, 20, 2400),
		    ('large', 180, 300, 30, 5400)
    `)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	kpi := &VMLifeSpanKPI{}
	if err := kpi.Init(testDB, conf.NewRawOpts("{}")); err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	ch := make(chan prometheus.Metric, 10)
	kpi.Collect(ch)
	close(ch)

	metricsCount := 0
	for range ch {
		metricsCount++
	}

	if metricsCount == 0 {
		t.Errorf("expected metrics to be collected, got %d", metricsCount)
	}
}
