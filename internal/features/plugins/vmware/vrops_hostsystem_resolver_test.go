// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package vmware

import (
	"testing"

	"github.com/cobaltcore-dev/cortex/internal/sync/openstack"
	"github.com/cobaltcore-dev/cortex/internal/sync/prometheus"
	testlibDB "github.com/cobaltcore-dev/cortex/testlib/db"
)

func TestVROpsHostsystemResolver_Init(t *testing.T) {
	mockDB := testlibDB.NewSqliteMockDB()
	mockDB.Init(t)
	defer mockDB.Close()

	extractor := &VROpsHostsystemResolver{}
	if err := extractor.Init(*mockDB.DB, nil); err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	// Verify the table was created
	if !mockDB.TableExists(ResolvedVROpsHostsystem{}) {
		t.Error("expected table to be created")
	}
}

func TestVROpsHostsystemResolver_Extract(t *testing.T) {
	mockDB := testlibDB.NewSqliteMockDB()
	mockDB.Init(t)
	defer mockDB.Close()

	// Create dependency tables
	if err := mockDB.CreateTable(
		mockDB.AddTable(prometheus.VROpsVMMetric{}),
		mockDB.AddTable(openstack.Server{}),
		mockDB.AddTable(openstack.Hypervisor{}),
	); err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	// Insert mock data into the metrics table
	_, err := mockDB.Exec(`
        INSERT INTO vrops_vm_metrics (hostsystem, instance_uuid)
        VALUES
            ('hostsystem1', 'uuid1'),
            ('hostsystem2', 'uuid2')
    `)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	// Insert mock data into the openstack_servers table
	_, err = mockDB.Exec(`
        INSERT INTO openstack_servers (id, os_ext_srv_attr_hypervisor_hostname)
        VALUES
            ('uuid1', 'hostname1'),
            ('uuid2', 'hostname2')
    `)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	// Insert mock data into the openstack_hypervisors table
	_, err = mockDB.Exec(`
        INSERT INTO openstack_hypervisors (hostname, service_host)
        VALUES
            ('hostname1', 'service_host1'),
            ('hostname2', 'service_host2')
    `)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	extractor := &VROpsHostsystemResolver{}
	if err := extractor.Init(*mockDB.DB, nil); err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if err := extractor.Extract(); err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	// Verify the data was inserted into the feature_vrops_resolved_hostsystem table
	var resolvedHostsystems []ResolvedVROpsHostsystem
	_, err = mockDB.Select(&resolvedHostsystems, "SELECT * FROM feature_vrops_resolved_hostsystem")
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if len(resolvedHostsystems) != 2 {
		t.Errorf("expected 2 rows, got %d", len(resolvedHostsystems))
	}
	expected := map[string]string{
		"hostsystem1": "service_host1",
		"hostsystem2": "service_host2",
	}
	for _, r := range resolvedHostsystems {
		if expected[r.VROpsHostsystem] != r.NovaComputeHost {
			t.Errorf("expected nova_compute_host for vrops_hostsystem %s to be %s, got %s", r.VROpsHostsystem, expected[r.VROpsHostsystem], r.NovaComputeHost)
		}
	}
}
