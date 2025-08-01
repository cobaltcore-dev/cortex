// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package vmware

import (
	"testing"

	"github.com/cobaltcore-dev/cortex/internal/conf"
	"github.com/cobaltcore-dev/cortex/internal/db"
	"github.com/cobaltcore-dev/cortex/internal/sync/openstack/nova"
	"github.com/cobaltcore-dev/cortex/internal/sync/prometheus"
	testlibDB "github.com/cobaltcore-dev/cortex/testlib/db"
)

func TestVROpsProjectNoisinessExtractor_Init(t *testing.T) {
	dbEnv := testlibDB.SetupDBEnv(t)
	testDB := db.DB{DbMap: dbEnv.DbMap}
	defer testDB.Close()
	defer dbEnv.Close()

	extractor := &VROpsProjectNoisinessExtractor{}

	config := conf.FeatureExtractorConfig{
		Name:           "vrops_project_noisiness_extractor",
		Options:        conf.NewRawOpts("{}"),
		RecencySeconds: nil,
	}
	if err := extractor.Init(testDB, config); err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	// Will fail when the table does not exist
	table := VROpsProjectNoisiness{}.TableName()
	err := testDB.SelectOne(&VROpsProjectNoisiness{}, "SELECT * FROM "+table+" LIMIT 1")
	if err == nil {
		t.Fatalf("expected error, got nil")
	}
}

func TestVROpsProjectNoisinessExtractor_Extract(t *testing.T) {
	dbEnv := testlibDB.SetupDBEnv(t)
	testDB := db.DB{DbMap: dbEnv.DbMap}
	defer testDB.Close()
	defer dbEnv.Close()

	// Create dependency tables
	if err := testDB.CreateTable(
		testDB.AddTable(prometheus.VROpsVMMetric{}),
		testDB.AddTable(nova.Server{}),
		testDB.AddTable(nova.Hypervisor{}),
	); err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	// Insert mock data into the metrics table
	if _, err := testDB.Exec(`
	INSERT INTO vrops_vm_metrics (name, project, value, instance_uuid)
	VALUES
		('vrops_virtualmachine_cpu_demand_ratio', 'project1', 50, 'uuid1'),
		('vrops_virtualmachine_cpu_demand_ratio', 'project1', 60, 'uuid2'),
		('vrops_virtualmachine_cpu_demand_ratio', 'project2', 70, 'uuid3')
	`); err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	// Insert mock data into the openstack_servers table
	if _, err := testDB.Exec(`
	INSERT INTO openstack_servers (id, tenant_id, os_ext_srv_attr_hypervisor_hostname)
	VALUES
		('uuid1', 'project1', 'host1'),
		('uuid2', 'project1', 'host2'),
		('uuid3', 'project2', 'host1')
	`); err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	// Insert mock data into the openstack_hypervisors table
	if _, err := testDB.Exec(`
	INSERT INTO openstack_hypervisors (id, hostname, service_host)
	VALUES
		(1, 'host1', 'service_host1'),
		(2, 'host2', 'service_host2')
	`); err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	extractor := &VROpsProjectNoisinessExtractor{}

	config := conf.FeatureExtractorConfig{
		Name:           "vrops_project_noisines_extractor",
		Options:        conf.NewRawOpts("{}"),
		RecencySeconds: nil,
	}

	if err := extractor.Init(testDB, config); err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if _, err := extractor.Extract(); err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	// Verify the data was inserted into the feature_vrops_project_noisiness table
	var noisiness []VROpsProjectNoisiness
	q := `SELECT * FROM feature_vrops_project_noisiness ORDER BY project, compute_host`
	if _, err := testDB.Select(&noisiness, q); err != nil {
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
