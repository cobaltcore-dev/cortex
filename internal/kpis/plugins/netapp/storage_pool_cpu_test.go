// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package netapp

import (
	"testing"

	"github.com/cobaltcore-dev/cortex/internal/conf"
	"github.com/cobaltcore-dev/cortex/internal/db"
	"github.com/cobaltcore-dev/cortex/internal/extractor/plugins/netapp"
	testlibDB "github.com/cobaltcore-dev/cortex/testlib/db"
	"github.com/prometheus/client_golang/prometheus"
)

func TestNetAppStoragePoolCPUUsageKPI_Init(t *testing.T) {
	dbEnv := testlibDB.SetupDBEnv(t)
	testDB := db.DB{DbMap: dbEnv.DbMap}
	defer testDB.Close()
	defer dbEnv.Close()

	kpi := &NetAppStoragePoolCPUUsageKPI{}
	if err := kpi.Init(testDB, conf.NewRawOpts("{}")); err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
}

func TestNetAppStoragePoolCPUUsageKPI_Collect(t *testing.T) {
	dbEnv := testlibDB.SetupDBEnv(t)
	testDB := db.DB{DbMap: dbEnv.DbMap}
	defer testDB.Close()
	defer dbEnv.Close()

	// Create dependency tables
	if err := testDB.CreateTable(
		testDB.AddTable(netapp.StoragePoolCPUUsage{}),
	); err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	storagePoolCPUUsage := []any{
		&netapp.StoragePoolCPUUsage{StoragePoolName: "pool1", MaxCPUUsagePct: 80.5, AvgCPUUsagePct: 60.0},
		&netapp.StoragePoolCPUUsage{StoragePoolName: "pool2", MaxCPUUsagePct: 90.0, AvgCPUUsagePct: 70.0},
	}
	if err := testDB.Insert(storagePoolCPUUsage...); err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	kpi := &NetAppStoragePoolCPUUsageKPI{}
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
