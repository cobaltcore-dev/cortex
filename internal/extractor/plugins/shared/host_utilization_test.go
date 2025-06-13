// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package shared

import (
	"testing"

	"github.com/cobaltcore-dev/cortex/internal/conf"
	"github.com/cobaltcore-dev/cortex/internal/db"
	"github.com/cobaltcore-dev/cortex/internal/sync/openstack/nova"
	testlibDB "github.com/cobaltcore-dev/cortex/testlib/db"
)

func TestHostUtilizationExtractor_Init(t *testing.T) {
	dbEnv := testlibDB.SetupDBEnv(t)
	testDB := db.DB{DbMap: dbEnv.DbMap}
	defer testDB.Close()
	defer dbEnv.Close()

	extractor := &HostUtilizationExtractor{}
	config := conf.FeatureExtractorConfig{
		Name:           "host_utilization_extractor",
		Options:        conf.NewRawOpts("{}"),
		RecencySeconds: nil, // No recency for this test
	}
	if err := extractor.Init(testDB, config); err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	if !testDB.TableExists(HostUtilization{}) {
		t.Error("expected table to be created")
	}
}

func TestHostUtilizationExtractor_Extract(t *testing.T) {
	dbEnv := testlibDB.SetupDBEnv(t)
	testDB := db.DB{DbMap: dbEnv.DbMap}
	defer testDB.Close()
	defer dbEnv.Close()

	// Create dependency tables
	if err := testDB.CreateTable(
		testDB.AddTable(nova.Hypervisor{}),
	); err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	// Insert mock data into the hypervisors and flavors tables
	hs := []any{
		&nova.Hypervisor{ID: "1", Hostname: "hostname1", ServiceHost: "host1", MemoryMBUsed: 16000, MemoryMB: 32000, VCPUs: 16, VCPUsUsed: 4, LocalGBUsed: 200, LocalGB: 400},
		&nova.Hypervisor{ID: "2", Hostname: "hostname2", ServiceHost: "host2", MemoryMBUsed: 32000, MemoryMB: 64000, VCPUs: 32, VCPUsUsed: 8, LocalGBUsed: 400, LocalGB: 800},
		&nova.Hypervisor{ID: "3", Hostname: "hostname3", ServiceHost: "host3", MemoryMBUsed: 32000, MemoryMB: 64000, VCPUs: 32, VCPUsUsed: 8, LocalGBUsed: 400, LocalGB: 800, HypervisorType: "ironic"}, // Should be ignored
	}
	if err := testDB.Insert(hs...); err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	extractor := &HostUtilizationExtractor{}
	config := conf.FeatureExtractorConfig{
		Name:           "host_utilization_extractor",
		Options:        conf.NewRawOpts("{}"),
		RecencySeconds: nil, // No recency for this test
	}
	if err := extractor.Init(testDB, config); err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if _, err := extractor.Extract(); err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	// Verify the data was inserted into the feature_host_utilization table
	var utilizations []HostUtilization
	_, err := testDB.Select(&utilizations, "SELECT * FROM feature_host_utilization")
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	if len(utilizations) != 2 {
		t.Errorf("expected 2 rows, got %d", len(utilizations))
	}

	// Compare expected values with actual values in utilizations
	expected := []HostUtilization{
		{
			ComputeHost:      "host1",
			RAMUtilizedPct:   50.0,
			VCPUsUtilizedPct: 25.0,
			DiskUtilizedPct:  50.0,
		},
		{
			ComputeHost:      "host2",
			RAMUtilizedPct:   50.0,
			VCPUsUtilizedPct: 25.0,
			DiskUtilizedPct:  50.0,
		},
	}

	for i, utilization := range utilizations {
		if utilization != expected[i] {
			t.Errorf("expected %+v, got %+v", expected[i], utilization)
		}
	}
}
