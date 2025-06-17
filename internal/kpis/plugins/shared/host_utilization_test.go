// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package shared

import (
	"testing"

	"github.com/cobaltcore-dev/cortex/internal/extractor/plugins/shared"
	"github.com/cobaltcore-dev/cortex/internal/sync/openstack/nova"

	"github.com/cobaltcore-dev/cortex/internal/conf"
	"github.com/cobaltcore-dev/cortex/internal/db"
	testlibDB "github.com/cobaltcore-dev/cortex/testlib/db"
	"github.com/prometheus/client_golang/prometheus"
)

func TestHostUtilizationKPI_Init(t *testing.T) {
	dbEnv := testlibDB.SetupDBEnv(t)
	testDB := db.DB{DbMap: dbEnv.DbMap}
	defer testDB.Close()
	defer dbEnv.Close()

	kpi := &HostUtilizationKPI{}
	if err := kpi.Init(testDB, conf.NewRawOpts("{}")); err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
}

func TestHostUtilizationKPI_Collect(t *testing.T) {
	dbEnv := testlibDB.SetupDBEnv(t)
	testDB := db.DB{DbMap: dbEnv.DbMap}
	defer testDB.Close()
	defer dbEnv.Close()

	// Create dependency tables
	if err := testDB.CreateTable(
		testDB.AddTable(shared.HostUtilization{}),
		testDB.AddTable(nova.Aggregate{}),
	); err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	// Insert mock data into the host space table
	hs := []any{
		&shared.HostUtilization{ComputeHost: "host1", RAMUtilizedPct: 50, VCPUsUtilizedPct: 50, DiskUtilizedPct: 50, TotalMemoryAllocatableMB: 1000, TotalVCPUsAllocatable: 100, TotalDiskAllocatableGB: 100},
		&shared.HostUtilization{ComputeHost: "host2", RAMUtilizedPct: 80, VCPUsUtilizedPct: 75, DiskUtilizedPct: 80, TotalMemoryAllocatableMB: 1000, TotalVCPUsAllocatable: 100, TotalDiskAllocatableGB: 100},
	}
	if err := testDB.Insert(hs...); err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	availabilityZone1 := "zone1"
	availabilityZone2 := "zone2"
	as := []any{
		&nova.Aggregate{Name: "zone1", AvailabilityZone: &availabilityZone1, ComputeHost: "host1"},
		&nova.Aggregate{Name: "zone2", AvailabilityZone: &availabilityZone2, ComputeHost: "host2"},
		&nova.Aggregate{Name: "something-else", AvailabilityZone: &availabilityZone2, ComputeHost: "host2"},
	}
	if err := testDB.Insert(as...); err != nil {
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
