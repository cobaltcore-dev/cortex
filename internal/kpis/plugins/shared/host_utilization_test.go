// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package shared

import (
	"testing"

	"github.com/cobaltcore-dev/cortex/internal/conf"
	"github.com/cobaltcore-dev/cortex/internal/db"
	"github.com/cobaltcore-dev/cortex/internal/sync/openstack/nova"
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
		testDB.AddTable(nova.Hypervisor{}),
	); err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	// Insert mock data into the hypervisors table
	_, err := testDB.Exec(`
        INSERT INTO openstack_hypervisors (
            id, service_host, hostname, state, status, hypervisor_type, hypervisor_version, host_ip, service_id, service_disabled_reason, vcpus, memory_mb, local_gb, vcpus_used, memory_mb_used, local_gb_used, free_ram_mb, free_disk_gb, current_workload, running_vms, disk_available_least, cpu_info
        )
        VALUES
            (1, 'host1', 'hypervisor1', 'active', 'enabled', 'QEMU', 1000, '192.168.1.1', 1, 'none', 16, 32000, 1000, 8, 16000, 500, 16000, 500, 0, 10, 100, 'Intel'),
            (2, 'host2', 'hypervisor2', 'active', 'enabled', 'QEMU', 1000, '192.168.1.2', 2, 'none', 32, 64000, 2000, 16, 32000, 1000, 32000, 1000, 0, 20, 200, 'AMD')
    `)
	if err != nil {
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
