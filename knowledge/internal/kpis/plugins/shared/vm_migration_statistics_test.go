// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package shared

import (
	"testing"

	"github.com/cobaltcore-dev/cortex/knowledge/api/features/shared"
	"github.com/cobaltcore-dev/cortex/pkg/conf"
	"github.com/cobaltcore-dev/cortex/pkg/db"
	testlibDB "github.com/cobaltcore-dev/cortex/pkg/db/testing"
	"github.com/prometheus/client_golang/prometheus"
)

func TestVMMigrationStatisticsKPI_Init(t *testing.T) {
	dbEnv := testlibDB.SetupDBEnv(t)
	testDB := db.DB{DbMap: dbEnv.DbMap}
	defer dbEnv.Close()
	kpi := &VMMigrationStatisticsKPI{}
	if err := kpi.Init(testDB, conf.NewRawOpts("{}")); err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
}

func TestVMMigrationStatisticsKPI_Collect(t *testing.T) {
	dbEnv := testlibDB.SetupDBEnv(t)
	testDB := db.DB{DbMap: dbEnv.DbMap}
	defer dbEnv.Close()
	// Create dependency tables
	if err := testDB.CreateTable(
		testDB.AddTable(shared.VMHostResidencyHistogramBucket{}),
	); err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	vmHostResidency := []any{
		&shared.VMHostResidencyHistogramBucket{FlavorName: "small", Bucket: 60, Value: 100, Count: 10, Sum: 600},
		&shared.VMHostResidencyHistogramBucket{FlavorName: "medium", Bucket: 120, Value: 200, Count: 20, Sum: 2400},
		&shared.VMHostResidencyHistogramBucket{FlavorName: "large", Bucket: 180, Value: 300, Count: 30, Sum: 5400},
	}

	if err := testDB.Insert(vmHostResidency...); err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	kpi := &VMMigrationStatisticsKPI{}
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
