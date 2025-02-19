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

func TestNodeExporterHostCPUUsageExtractor_Init(t *testing.T) {
	dbEnv := testlibDB.SetupDBEnv(t)
	testDB := db.DB{DbMap: dbEnv.DbMap}
	defer testDB.Close()
	defer dbEnv.Close()

	extractor := &NodeExporterHostCPUUsageExtractor{}
	if err := extractor.Init(testDB, conf.NewRawOpts("")); err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	if !testDB.TableExists(NodeExporterHostCPUUsage{}) {
		t.Error("expected table to be created")
	}
}

func TestNodeExporterHostCPUUsageExtractor_Extract(t *testing.T) {
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
	_, err := testDB.Exec(`
        INSERT INTO node_exporter_metrics (node, name, value)
        VALUES
            ('node1', 'node_exporter_cpu_usage_pct', 20.0),
            ('node2', 'node_exporter_cpu_usage_pct', 30.0),
            ('node1', 'node_exporter_cpu_usage_pct', 40.0)
    `)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	extractor := &NodeExporterHostCPUUsageExtractor{}
	if err := extractor.Init(testDB, conf.NewRawOpts("")); err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if _, err = extractor.Extract(); err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	// Verify the data was inserted into the feature_host_cpu_usage table
	var usages []NodeExporterHostCPUUsage
	_, err = testDB.Select(&usages, "SELECT * FROM feature_host_cpu_usage")
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	if len(usages) != 2 {
		t.Errorf("expected 2 rows, got %d", len(usages))
	}
	expected := map[string]struct {
		AvgCPUUsage float64
		MaxCPUUsage float64
	}{
		"node1": {AvgCPUUsage: 30.0, MaxCPUUsage: 40.0}, // Average of 20.0 and 40.0, Max of 40.0
		"node2": {AvgCPUUsage: 30.0, MaxCPUUsage: 30.0}, // Single value of 30.0
	}
	for _, u := range usages {
		if expected[u.ComputeHost].AvgCPUUsage != u.AvgCPUUsage {
			t.Errorf(
				"expected avg_cpu_usage for compute_host %s to be %f, got %f",
				u.ComputeHost, expected[u.ComputeHost].AvgCPUUsage, u.AvgCPUUsage,
			)
		}
		if expected[u.ComputeHost].MaxCPUUsage != u.MaxCPUUsage {
			t.Errorf(
				"expected max_cpu_usage for compute_host %s to be %f, got %f",
				u.ComputeHost, expected[u.ComputeHost].MaxCPUUsage, u.MaxCPUUsage,
			)
		}
	}
}
