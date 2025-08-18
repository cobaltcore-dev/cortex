// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package kvm

import (
	"testing"

	"github.com/cobaltcore-dev/cortex/internal/conf"
	"github.com/cobaltcore-dev/cortex/internal/db"
	"github.com/cobaltcore-dev/cortex/internal/sync/prometheus"
	testlibDB "github.com/cobaltcore-dev/cortex/testlib/db"
)

func TestNodeExporterHostMemoryActiveExtractor_Init(t *testing.T) {
	dbEnv := testlibDB.SetupDBEnv(t)
	testDB := db.DB{DbMap: dbEnv.DbMap}
	defer testDB.Close()
	defer dbEnv.Close()

	extractor := &NodeExporterHostMemoryActiveExtractor{}
	config := conf.FeatureExtractorConfig{
		Name:           "node_exporter_host_memory_active_extractor",
		Options:        conf.NewRawOpts("{}"),
		RecencySeconds: nil,
	}
	if err := extractor.Init(testDB, config); err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	if !testDB.TableExists(NodeExporterHostMemoryActive{}) {
		t.Error("expected table to be created")
	}
}

func TestNodeExporterHostMemoryActiveExtractor_Extract(t *testing.T) {
	dbEnv := testlibDB.SetupDBEnv(t)
	testDB := db.DB{DbMap: dbEnv.DbMap}
	defer testDB.Close()
	defer dbEnv.Close()

	// Create dependency tables
	if err := testDB.CreateTable(
		testDB.AddTable(prometheus.NodeExporterMetric{}),
	); err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	// Insert mock data into the node_exporter_metrics table

	nodeExporterMetrics := []any{
		&prometheus.NodeExporterMetric{Node: "node1", Name: "node_exporter_cpu_usage_pct", Value: 20.0},
		&prometheus.NodeExporterMetric{Node: "node2", Name: "node_exporter_cpu_usage_pct", Value: 30.0},
		&prometheus.NodeExporterMetric{Node: "node1", Name: "node_exporter_cpu_usage_pct", Value: 40.0},
	}

	if err := testDB.Insert(nodeExporterMetrics...); err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	extractor := &NodeExporterHostMemoryActiveExtractor{}
	config := conf.FeatureExtractorConfig{
		Name:           "node_exporter_host_memory_active_extractor",
		Options:        conf.NewRawOpts("{}"),
		RecencySeconds: nil,
	}
	if err := extractor.Init(testDB, config); err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if _, err := extractor.Extract(); err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	// Verify the data was inserted into the feature_host_memory_active table
	var usages []NodeExporterHostMemoryActive
	_, err := testDB.Select(&usages, "SELECT * FROM "+NodeExporterHostMemoryActive{}.TableName())
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	if len(usages) != 2 {
		t.Errorf("expected 2 rows, got %d", len(usages))
	}
	expected := map[string]struct {
		AvgMemoryActive float64
		MaxMemoryActive float64
	}{
		"node1": {AvgMemoryActive: 30.0, MaxMemoryActive: 40.0}, // Average of 20.0 and 40.0, Max of 40.0
		"node2": {AvgMemoryActive: 30.0, MaxMemoryActive: 30.0}, // Single value of 30.0
	}
	for _, u := range usages {
		if expected[u.ComputeHost].AvgMemoryActive != u.AvgMemoryActive {
			t.Errorf(
				"expected avg_cpu_usage for compute_host %s to be %f, got %f",
				u.ComputeHost, expected[u.ComputeHost].AvgMemoryActive, u.AvgMemoryActive,
			)
		}
		if expected[u.ComputeHost].MaxMemoryActive != u.MaxMemoryActive {
			t.Errorf(
				"expected max_cpu_usage for compute_host %s to be %f, got %f",
				u.ComputeHost, expected[u.ComputeHost].MaxMemoryActive, u.MaxMemoryActive,
			)
		}
	}
}
