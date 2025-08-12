// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package netapp

import (
	"testing"

	"github.com/cobaltcore-dev/cortex/internal/conf"
	"github.com/cobaltcore-dev/cortex/internal/db"
	"github.com/cobaltcore-dev/cortex/internal/sync/openstack/manila"
	testlibDB "github.com/cobaltcore-dev/cortex/testlib/db"
)

func TestStoragePoolCPUUsageExtractor_Init(t *testing.T) {
	dbEnv := testlibDB.SetupDBEnv(t)
	testDB := db.DB{DbMap: dbEnv.DbMap}
	defer testDB.Close()
	defer dbEnv.Close()

	extractor := &StoragePoolCPUUsageExtractor{}
	config := conf.FeatureExtractorConfig{
		Name:           "netapp_storage_pool_cpu_usage_extractor",
		Options:        conf.NewRawOpts("{}"),
		RecencySeconds: nil, // No recency for this test
	}
	if err := extractor.Init(testDB, config); err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	if !testDB.TableExists(StoragePoolCPUUsage{}) {
		t.Error("expected table to be created")
	}
}

func TestStoragePoolCPUUsageExtractor_Extract(t *testing.T) {
	dbEnv := testlibDB.SetupDBEnv(t)
	testDB := db.DB{DbMap: dbEnv.DbMap}
	defer testDB.Close()
	defer dbEnv.Close()

	// Create dependency tables
	if err := testDB.CreateTable(
		testDB.AddTable(manila.StoragePool{}),
	); err != nil {
		t.Fatalf("failed to create openstack_manila_storage_pools: %v", err)
	}
	if _, err := testDB.Exec(`
        CREATE TABLE netapp_aggregate_labels_metrics (
            aggr TEXT,
            node TEXT
        );
    `); err != nil {
		t.Fatalf("failed to create netapp_aggregate_labels_metrics: %v", err)
	}
	if _, err := testDB.Exec(`
        CREATE TABLE netapp_node_metrics (
            node TEXT,
            name TEXT,
            value FLOAT
        );
    `); err != nil {
		t.Fatalf("failed to create netapp_node_metrics: %v", err)
	}

	// Insert mock data
	_, err := testDB.Exec(`
        INSERT INTO openstack_manila_storage_pools (name, pool) VALUES
        ('pool1', 'aggr1'),
        ('pool2', 'aggr2')
    `)
	if err != nil {
		t.Fatalf("failed to insert openstack_manila_storage_pools: %v", err)
	}
	_, err = testDB.Exec(`
        INSERT INTO netapp_aggregate_labels_metrics (aggr, node) VALUES
        ('aggr1', 'node1'),
        ('aggr2', 'node2')
    `)
	if err != nil {
		t.Fatalf("failed to insert netapp_aggregate_labels_metrics: %v", err)
	}
	_, err = testDB.Exec(`
        INSERT INTO netapp_node_metrics (node, name, value) VALUES
        ('node1', 'netapp_node_cpu_busy', 10.5),
        ('node1', 'netapp_node_cpu_busy', 20.0),
        ('node2', 'netapp_node_cpu_busy', 30.0),
        ('node2', 'netapp_node_cpu_busy', 40.0),
        ('node2', 'other_metric', 99.0)
    `)
	if err != nil {
		t.Fatalf("failed to insert netapp_node_metrics: %v", err)
	}

	extractor := &StoragePoolCPUUsageExtractor{}
	config := conf.FeatureExtractorConfig{
		Name:           "netapp_storage_pool_cpu_usage_extractor",
		Options:        conf.NewRawOpts("{}"),
		RecencySeconds: nil, // No recency for this test
	}
	if err := extractor.Init(testDB, config); err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	features, err := extractor.Extract()
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	var cpuUsages []StoragePoolCPUUsage
	for _, f := range features {
		cpuUsages = append(cpuUsages, f.(StoragePoolCPUUsage))
	}

	expected := []StoragePoolCPUUsage{
		{StoragePoolName: "pool1", AvgCPUUsagePct: 15.25, MaxCPUUsagePct: 20.0},
		{StoragePoolName: "pool2", AvgCPUUsagePct: 35.0, MaxCPUUsagePct: 40.0},
	}

	if len(cpuUsages) != len(expected) {
		t.Errorf("expected %d rows, got %d", len(expected), len(cpuUsages))
	}

	for _, exp := range expected {
		found := false
		for _, got := range cpuUsages {
			if got.StoragePoolName == exp.StoragePoolName &&
				got.AvgCPUUsagePct == exp.AvgCPUUsagePct &&
				got.MaxCPUUsagePct == exp.MaxCPUUsagePct {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("expected %+v, got %+v", exp, cpuUsages)
		}
	}
}
