// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package vmware

import (
	"testing"

	"github.com/cobaltcore-dev/cortex/extractor/api/features/vmware"
	"github.com/cobaltcore-dev/cortex/extractor/internal/conf"
	libconf "github.com/cobaltcore-dev/cortex/lib/conf"
	"github.com/cobaltcore-dev/cortex/lib/db"
	"github.com/cobaltcore-dev/cortex/sync/api/objects/prometheus"
	testlibDB "github.com/cobaltcore-dev/cortex/testlib/db"
)

func TestVROpsHostsystemContentionShortTermExtractor_Init(t *testing.T) {
	dbEnv := testlibDB.SetupDBEnv(t)
	testDB := db.DB{DbMap: dbEnv.DbMap}
	defer testDB.Close()
	defer dbEnv.Close()

	extractor := &VROpsHostsystemContentionShortTermExtractor{}

	config := conf.FeatureExtractorConfig{
		Name:           "vrops_hostsystem_contention_short_term_extractor",
		Options:        libconf.NewRawOpts("{}"),
		RecencySeconds: nil,
	}
	if err := extractor.Init(testDB, config); err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	if !testDB.TableExists(vmware.VROpsHostsystemContentionShortTerm{}) {
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
		testDB.AddTable(vmware.ResolvedVROpsHostsystem{}),
		testDB.AddTable(prometheus.VROpsHostMetric{}),
	); err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	vropsHostMetrics := []any{
		&prometheus.VROpsHostMetric{HostSystem: "hostsystem1", Name: "vrops_hostsystem_cpu_contention_short_term_percentage", Value: 30.0},
		&prometheus.VROpsHostMetric{HostSystem: "hostsystem2", Name: "vrops_hostsystem_cpu_contention_short_term_percentage", Value: 40.0},
		&prometheus.VROpsHostMetric{HostSystem: "hostsystem1", Name: "vrops_hostsystem_cpu_contention_short_term_percentage", Value: 50.0},
	}
	if err := testDB.Insert(vropsHostMetrics...); err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	vropsResolvedHostsystems := []any{
		&vmware.ResolvedVROpsHostsystem{VROpsHostsystem: "hostsystem1", NovaComputeHost: "compute_host1"},
		&vmware.ResolvedVROpsHostsystem{VROpsHostsystem: "hostsystem2", NovaComputeHost: "compute_host2"},
	}
	if err := testDB.Insert(vropsResolvedHostsystems...); err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	extractor := &VROpsHostsystemContentionShortTermExtractor{}
	config := conf.FeatureExtractorConfig{
		Name:           "vrops_hostsystem_contention_short_term_extractor",
		Options:        libconf.NewRawOpts("{}"),
		RecencySeconds: nil,
	}
	if err := extractor.Init(testDB, config); err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if _, err := extractor.Extract(); err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	// Verify the data was inserted into the feature_vrops_hostsystem_contention table
	var contentions []vmware.VROpsHostsystemContentionShortTerm
	table := vmware.VROpsHostsystemContentionShortTerm{}.TableName()
	_, err := testDB.Select(&contentions, "SELECT * FROM "+table)
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
