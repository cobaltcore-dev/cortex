// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package shared

import (
	"testing"

	"github.com/cobaltcore-dev/cortex/internal/extractor/plugins/shared"

	"github.com/cobaltcore-dev/cortex/internal/conf"
	"github.com/cobaltcore-dev/cortex/internal/db"
	testlibDB "github.com/cobaltcore-dev/cortex/testlib/db"
	"github.com/prometheus/client_golang/prometheus"
)

func TestHostCapacityKPI_Init(t *testing.T) {
	dbEnv := testlibDB.SetupDBEnv(t)
	testDB := db.DB{DbMap: dbEnv.DbMap}
	defer testDB.Close()
	defer dbEnv.Close()

	kpi := &HostUtilizationKPI{}
	if err := kpi.Init(testDB, conf.NewRawOpts("{}")); err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
}

func TestHostCapacityKPI_Collect(t *testing.T) {
	dbEnv := testlibDB.SetupDBEnv(t)
	testDB := db.DB{DbMap: dbEnv.DbMap}
	defer testDB.Close()
	defer dbEnv.Close()

	// Create dependency tables
	if err := testDB.CreateTable(
		testDB.AddTable(shared.HostUtilization{}),
	); err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	// Insert mock data into the host space table
	hs := []any{
		&shared.HostUtilization{ComputeHost: "host1", RAMUtilizedPct: 50, VCPUsUtilizedPct: 50, DiskUtilizedPct: 50},
		&shared.HostUtilization{ComputeHost: "host2", RAMUtilizedPct: 80, VCPUsUtilizedPct: 75, DiskUtilizedPct: 80},
	}
	if err := testDB.Insert(hs...); err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	kpi := &HostUtilizationKPI{}
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
