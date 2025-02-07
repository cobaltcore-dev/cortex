// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package vmware

import (
	"testing"

	"github.com/cobaltcore-dev/cortex/internal/sync/openstack"
	"github.com/cobaltcore-dev/cortex/internal/sync/prometheus"
	testlibDB "github.com/cobaltcore-dev/cortex/testlib/db"
	"github.com/go-pg/pg/v10/orm"
)

func TestVROpsProjectNoisinessExtractor_Init(t *testing.T) {
	mockDB := testlibDB.NewMockDB()
	mockDB.Init()
	defer mockDB.Close()

	extractor := &VROpsProjectNoisinessExtractor{}
	if err := extractor.Init(&mockDB, nil); err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	// Will fail when the table does not exist
	if _, err := mockDB.Get().Model((*VROpsProjectNoisiness)(nil)).Exists(); err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
}

func TestVROpsProjectNoisinessExtractor_Extract(t *testing.T) {
	mockDB := testlibDB.NewMockDB()
	mockDB.Init()
	defer mockDB.Close()

	// Create dependency tables
	deps := []interface{}{
		(*prometheus.VROpsVMMetric)(nil),
		(*openstack.Server)(nil),
		(*openstack.Hypervisor)(nil),
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
	if _, err := mockDB.Get().Exec(`
	INSERT INTO vrops_vm_metrics (name, project, value, instance_uuid)
	VALUES
		('vrops_virtualmachine_cpu_demand_ratio', 'project1', 50, 'uuid1'),
		('vrops_virtualmachine_cpu_demand_ratio', 'project1', 60, 'uuid2'),
		('vrops_virtualmachine_cpu_demand_ratio', 'project2', 70, 'uuid3')
	`); err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	// Insert mock data into the openstack_servers table
	if _, err := mockDB.Get().Exec(`
	INSERT INTO openstack_servers (id, tenant_id, os_ext_srv_attr_hypervisor_hostname)
	VALUES
		('uuid1', 'project1', 'host1'),
		('uuid2', 'project1', 'host2'),
		('uuid3', 'project2', 'host1')
	`); err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	// Insert mock data into the openstack_hypervisors table
	if _, err := mockDB.Get().Exec(`
	INSERT INTO openstack_hypervisors (hostname, service_host)
	VALUES
		('host1', 'service_host1'),
		('host2', 'service_host2')
	`); err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	extractor := &VROpsProjectNoisinessExtractor{}
	if err := extractor.Init(&mockDB, nil); err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if err := extractor.Extract(); err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	// Verify the data was inserted into the feature_vrops_project_noisiness table
	var noisiness []VROpsProjectNoisiness
	q := `SELECT * FROM feature_vrops_project_noisiness ORDER BY project, compute_host`
	if _, err := mockDB.Get().Query(&noisiness, q); err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	expected := []VROpsProjectNoisiness{
		{Project: "project1", ComputeHost: "service_host1", AvgCPUOfProject: 55},
		{Project: "project1", ComputeHost: "service_host2", AvgCPUOfProject: 55},
		{Project: "project2", ComputeHost: "service_host1", AvgCPUOfProject: 70},
	}
	if len(noisiness) != len(expected) {
		t.Fatalf("expected %d rows, got %d", len(expected), len(noisiness))
	}
	for i, n := range noisiness {
		if n != expected[i] {
			t.Fatalf("expected %v, got %v", expected[i], n)
		}
	}
}
