// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package features

import (
	"testing"

	"github.com/cobaltcore-dev/cortex/internal/sync/openstack"
	"github.com/cobaltcore-dev/cortex/internal/sync/prometheus"
	"github.com/cobaltcore-dev/cortex/testlib"
	"github.com/go-pg/pg/v10/orm"
)

func TestVROpsHostsystemResolver_Init(t *testing.T) {
	mockDB := testlib.NewMockDB()
	mockDB.Init()
	defer mockDB.Close()

	resolver := NewVROpsHostsystemResolver(&mockDB, monitor{})

	if err := resolver.Init(); err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	// Verify the table was created
	if _, err := mockDB.Get().Model((*ResolvedVROpsHostsystem)(nil)).Exists(); err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
}

func TestVROpsHostsystemResolver_Extract(t *testing.T) {
	mockDB := testlib.NewMockDB()
	mockDB.Init()
	defer mockDB.Close()

	// Create dependency tables
	deps := []interface{}{
		(*prometheus.VROpsVMMetric)(nil),
		(*openstack.OpenStackServer)(nil),
		(*openstack.OpenStackHypervisor)(nil),
	}
	for _, dep := range deps {
		if err := mockDB.
			Get().
			Model(dep).
			CreateTable(&orm.CreateTableOptions{IfNotExists: true}); err != nil {
			panic(err)
		}
	}

	// Insert mock data into the metrics table
	_, err := mockDB.Get().Exec(`
        INSERT INTO vrops_vm_metrics (hostsystem, instance_uuid)
        VALUES
            ('hostsystem1', 'uuid1'),
            ('hostsystem2', 'uuid2')
    `)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	// Insert mock data into the openstack_servers table
	_, err = mockDB.Get().Exec(`
        INSERT INTO openstack_servers (id, os_ext_srv_attr_hypervisor_hostname)
        VALUES
            ('uuid1', 'hostname1'),
            ('uuid2', 'hostname2')
    `)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	// Insert mock data into the openstack_hypervisors table
	_, err = mockDB.Get().Exec(`
        INSERT INTO openstack_hypervisors (hostname, service_host)
        VALUES
            ('hostname1', 'service_host1'),
            ('hostname2', 'service_host2')
    `)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	// Create an instance of the resolver
	resolver := NewVROpsHostsystemResolver(&mockDB, monitor{})
	if err = resolver.Init(); err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	// Call the function to test
	err = resolver.Extract()
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	// Verify the data was inserted into the feature_vrops_resolved_hostsystem table
	var resolvedHostsystems []ResolvedVROpsHostsystem
	err = mockDB.Get().Model(&resolvedHostsystems).Select()
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
