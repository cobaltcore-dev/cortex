// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package shared

import (
	"testing"

	"github.com/cobaltcore-dev/cortex/knowledge/api/features/shared"
	"github.com/cobaltcore-dev/cortex/lib/conf"
	"github.com/cobaltcore-dev/cortex/lib/db"
	testlibDB "github.com/cobaltcore-dev/cortex/testlib/db"
	"github.com/prometheus/client_golang/prometheus"
)

func TestVMMigrationStatisticsKPI_Init(t *testing.T) {
	dbEnv := testlibDB.SetupDBEnv(t)
	testDB := db.DB{DbMap: dbEnv.DbMap}
	defer testDB.Close()
	defer dbEnv.Close()

	kpi := &VMMigrationStatisticsKPI{}
	if err := kpi.Init(testDB, conf.NewRawOpts("{}")); err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
}

func TestVMMigrationStatisticsKPI_Collect(t *testing.T) {
	dbEnv := testlibDB.SetupDBEnv(t)
	testDB := db.DB{DbMap: dbEnv.DbMap}
	defer testDB.Close()
	defer dbEnv.Close()

	// Create dependency tables
	if err := testDB.CreateTable(
		testDB.AddTable(shared.VMHostResidency{}),
	); err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	vmHostResidency := []any{
		&shared.VMHostResidency{Duration: 120, FlavorName: "small", InstanceUUID: "uuid1", MigrationUUID: "migration1", SourceHost: "host1", TargetHost: "host2", SourceNode: "node1", TargetNode: "node2", UserID: "user1", ProjectID: "project1", Type: "live-migration", Time: 1620000000},
		&shared.VMHostResidency{Duration: 300, FlavorName: "medium", InstanceUUID: "uuid2", MigrationUUID: "migration2", SourceHost: "host3", TargetHost: "host4", SourceNode: "node3", TargetNode: "node4", UserID: "user2", ProjectID: "project2", Type: "resize", Time: 1620000300},
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
