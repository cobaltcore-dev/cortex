// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package shared

import (
	"testing"

	"github.com/cobaltcore-dev/cortex/internal/conf"
	"github.com/cobaltcore-dev/cortex/internal/db"
	"github.com/cobaltcore-dev/cortex/internal/features/plugins/shared"
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
	_, err := testDB.Exec(`
        INSERT INTO feature_vm_host_residency (
            duration, flavor_id, flavor_name, instance_uuid, migration_uuid, source_host, target_host, source_node, target_node, user_id, project_id, type, time
        )
        VALUES
            (120, 'flavor1', 'small', 'uuid1', 'migration1', 'host1', 'host2', 'node1', 'node2', 'user1', 'project1', 'live-migration', 1620000000),
            (300, 'flavor2', 'medium', 'uuid2', 'migration2', 'host3', 'host4', 'node3', 'node4', 'user2', 'project2', 'resize', 1620000300)
    `)
	if err != nil {
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
