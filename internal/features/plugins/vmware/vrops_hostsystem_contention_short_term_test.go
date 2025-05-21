// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package vmware

import (
	"testing"

	"github.com/cobaltcore-dev/cortex/internal/conf"
	"github.com/cobaltcore-dev/cortex/internal/db"
	"github.com/cobaltcore-dev/cortex/internal/sync/prometheus"
	testlibDB "github.com/cobaltcore-dev/cortex/testlib/db"
)

func TestVROpsHostsystemContentionShortTermExtractor_Init(t *testing.T) {
	dbEnv := testlibDB.SetupDBEnv(t)
	testDB := db.DB{DbMap: dbEnv.DbMap}
	defer testDB.Close()
	defer dbEnv.Close()

	extractor := &VROpsHostsystemContentionShortTermExtractor{}
	if err := extractor.Init(testDB, conf.NewRawOpts("{}")); err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	if !testDB.TableExists(VROpsHostsystemContentionShortTerm{}) {
		t.Error("expected table to be created")
	}
}

func TestVROpsHostsystemContentionShortTermExtractor_Extract(t *testing.T) {
	dbEnv := testlibDB.SetupDBEnv(t)
	testDB := db.DB{DbMap: dbEnv.DbMap}
	defer testDB.Close()
	defer dbEnv.Close()

	// Create dependency tables
	if err := testDB.CreateTable(
		testDB.AddTable(ResolvedVROpsHostsystem{}),
		testDB.AddTable(prometheus.VROpsHostMetric{}),
	); err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	// Insert mock data into the vrops_host_metrics table
	_, err := testDB.Exec(`
        INSERT INTO vrops_host_metrics (hostsystem, name, value)
        VALUES
            ('hostsystem1', 'vrops_hostsystem_cpu_contention_short_term_percentage', 30.0),
            ('hostsystem2', 'vrops_hostsystem_cpu_contention_short_term_percentage', 40.0),
            ('hostsystem1', 'vrops_hostsystem_cpu_contention_short_term_percentage', 50.0)
    `)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	// Insert mock data into the feature_vrops_resolved_hostsystem table
	_, err = testDB.Exec(`
        INSERT INTO feature_vrops_resolved_hostsystem (vrops_hostsystem, nova_compute_host)
        VALUES
            ('hostsystem1', 'compute_host1'),
            ('hostsystem2', 'compute_host2')
    `)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	extractor := &VROpsHostsystemContentionShortTermExtractor{}
	if err := extractor.Init(testDB, conf.NewRawOpts("{}")); err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if _, err = extractor.Extract(); err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	// Verify the data was inserted into the feature_vrops_hostsystem_contention table
	var contentions []VROpsHostsystemContentionShortTerm
	_, err = testDB.Select(&contentions, "SELECT * FROM feature_vrops_hostsystem_contention_short_term")
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	if len(contentions) != 2 {
		t.Errorf("expected 2 rows, got %d", len(contentions))
	}
	expected := map[string]struct {
		AvgCPUContention float64
		MaxCPUContention float64
	}{
		"compute_host1": {AvgCPUContention: 40.0, MaxCPUContention: 50.0}, // Average of 30.0 and 50.0, Max of 50.0
		"compute_host2": {AvgCPUContention: 40.0, MaxCPUContention: 40.0}, // Single value of 40.0
	}
	for _, c := range contentions {
		if expected[c.ComputeHost].AvgCPUContention != c.AvgCPUContention {
			t.Errorf(
				"expected avg_cpu_contention for compute_host %s to be %f, got %f",
				c.ComputeHost, expected[c.ComputeHost].AvgCPUContention, c.AvgCPUContention,
			)
		}
		if expected[c.ComputeHost].MaxCPUContention != c.MaxCPUContention {
			t.Errorf(
				"expected max_cpu_contention for compute_host %s to be %f, got %f",
				c.ComputeHost, expected[c.ComputeHost].MaxCPUContention, c.MaxCPUContention,
			)
		}
	}
}
