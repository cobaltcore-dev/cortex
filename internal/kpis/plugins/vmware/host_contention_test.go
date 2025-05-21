// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package vmware

import (
	"testing"

	"github.com/cobaltcore-dev/cortex/internal/conf"
	"github.com/cobaltcore-dev/cortex/internal/db"
	"github.com/cobaltcore-dev/cortex/internal/features/plugins/vmware"
	testlibDB "github.com/cobaltcore-dev/cortex/testlib/db"
	"github.com/prometheus/client_golang/prometheus"
)

func TestVMwareHostContentionKPI_Init(t *testing.T) {
	dbEnv := testlibDB.SetupDBEnv(t)
	testDB := db.DB{DbMap: dbEnv.DbMap}
	defer testDB.Close()
	defer dbEnv.Close()

	kpi := &VMwareHostContentionKPI{}
	if err := kpi.Init(testDB, conf.NewRawOpts("{}")); err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
}

func TestVMwareHostContentionKPI_Collect(t *testing.T) {
	dbEnv := testlibDB.SetupDBEnv(t)
	testDB := db.DB{DbMap: dbEnv.DbMap}
	defer testDB.Close()
	defer dbEnv.Close()

	// Create dependency tables
	if err := testDB.CreateTable(
		testDB.AddTable(vmware.VROpsHostsystemContentionLongTerm{}),
	); err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	// Insert mock data into the feature_vrops_hostsystem_contention table
	_, err := testDB.Exec(`
        INSERT INTO feature_vrops_hostsystem_contention_long_term (
            compute_host, avg_cpu_contention, max_cpu_contention
        )
        VALUES
            ('host1', 10.5, 20.0),
            ('host2', 15.0, 25.0)
    `)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	kpi := &VMwareHostContentionKPI{}
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
