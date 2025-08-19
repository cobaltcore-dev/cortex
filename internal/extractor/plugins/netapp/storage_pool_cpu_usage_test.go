// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package netapp

import (
	"testing"

	"github.com/cobaltcore-dev/cortex/internal/conf"
	"github.com/cobaltcore-dev/cortex/internal/db"
	"github.com/cobaltcore-dev/cortex/internal/sync/openstack/manila"
	"github.com/cobaltcore-dev/cortex/internal/sync/prometheus"
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
		testDB.AddTable(prometheus.NetAppAggregateLabelsMetric{}),
		testDB.AddTable(prometheus.NetAppNodeMetric{}),
	); err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	manilaStoragePools := []any{
		&manila.StoragePool{Name: "pool1", Pool: "aggr1"},
		&manila.StoragePool{Name: "pool2", Pool: "aggr2"},
	}
	if err := testDB.Insert(manilaStoragePools...); err != nil {
		t.Fatalf("failed to insert manila storage pools: %v", err)
	}

	netappAggregateLabels := []any{
		&prometheus.NetAppAggregateLabelsMetric{Aggregate: "aggr1", Node: "node1"},
		&prometheus.NetAppAggregateLabelsMetric{Aggregate: "aggr2", Node: "node2"},
	}
	if err := testDB.Insert(netappAggregateLabels...); err != nil {
		t.Fatalf("failed to insert netapp_aggregate_labels_metrics: %v", err)
	}

	netAppNodeMetrics := []any{
		&prometheus.NetAppNodeMetric{Node: "node1", Name: "netapp_node_cpu_busy", Value: 10.5},
		&prometheus.NetAppNodeMetric{Node: "node1", Name: "netapp_node_cpu_busy", Value: 20.0},
		&prometheus.NetAppNodeMetric{Node: "node2", Name: "netapp_node_cpu_busy", Value: 30.0},
		&prometheus.NetAppNodeMetric{Node: "node2", Name: "netapp_node_cpu_busy", Value: 40.0},
		&prometheus.NetAppNodeMetric{Node: "node2", Name: "other_metric", Value: 99.0},
	}
	if err := testDB.Insert(netAppNodeMetrics...); err != nil {
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
