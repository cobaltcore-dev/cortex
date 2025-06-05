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

func TestHostSpaceExtractor_Init(t *testing.T) {
	dbEnv := testlibDB.SetupDBEnv(t)
	testDB := db.DB{DbMap: dbEnv.DbMap}
	defer testDB.Close()
	defer dbEnv.Close()

	extractor := &HostSpaceExtractor{}
	config := conf.FeatureExtractorConfig{
		Name:           "host_space_extractor",
		Options:        conf.NewRawOpts("{}"),
		RecencySeconds: nil, // No recency for this test
	}
	if err := extractor.Init(testDB, config); err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	if !testDB.TableExists(HostSpace{}) {
		t.Error("expected table to be created")
	}
}

func TestHostSpaceExtractor_Extract(t *testing.T) {
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
		&nova.Hypervisor{ID: "1", Hostname: "hostname1", ServiceHost: "host1", FreeRAMMB: 16000, MemoryMB: 32000, VCPUs: 16, VCPUsUsed: 4, FreeDiskGB: 200, LocalGB: 400},
		&nova.Hypervisor{ID: "2", Hostname: "hostname2", ServiceHost: "host2", FreeRAMMB: 32000, MemoryMB: 64000, VCPUs: 32, VCPUsUsed: 8, FreeDiskGB: 400, LocalGB: 800},
	}
	if err := testDB.Insert(hs...); err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	extractor := &HostSpaceExtractor{}
	config := conf.FeatureExtractorConfig{
		Name:           "host_space_extractor",
		Options:        conf.NewRawOpts("{}"),
		RecencySeconds: nil, // No recency for this test
	}
	if err := extractor.Init(testDB, config); err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if _, err := extractor.Extract(); err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	// Verify the data was inserted into the feature_host_space table
	var spaces []HostSpace
	_, err := testDB.Select(&spaces, "SELECT * FROM feature_host_space")
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	if len(spaces) != 2 {
		t.Errorf("expected 2 rows, got %d", len(spaces))
	}

	// Compare expected values with actual values in spaces
	expected := []HostSpace{
		{
			ComputeHost:  "host1",
			RAMLeftMB:    16000,
			RAMLeftPct:   50.0,
			VCPUsLeft:    12,
			VCPUsLeftPct: 75.0,
			DiskLeftGB:   200,
			DiskLeftPct:  50.0,
		},
		{
			ComputeHost:  "host2",
			RAMLeftMB:    32000,
			RAMLeftPct:   50.0,
			VCPUsLeft:    24,
			VCPUsLeftPct: 75.0,
			DiskLeftGB:   400,
			DiskLeftPct:  50.0,
		},
	}

	for i, space := range spaces {
		if space != expected[i] {
			t.Errorf("expected %+v, got %+v", expected[i], space)
		}
	}
}
