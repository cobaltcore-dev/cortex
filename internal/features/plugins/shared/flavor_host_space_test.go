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

func TestFlavorHostSpaceExtractor_Init(t *testing.T) {
	dbEnv := testlibDB.SetupDBEnv(t)
	testDB := db.DB{DbMap: dbEnv.DbMap}
	defer testDB.Close()
	defer dbEnv.Close()

	extractor := &FlavorHostSpaceExtractor{}
	if err := extractor.Init(testDB, conf.NewRawOpts("{}")); err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	if !testDB.TableExists(FlavorHostSpace{}) {
		t.Error("expected table to be created")
	}
}

func TestFlavorHostSpaceExtractor_Extract(t *testing.T) {
	dbEnv := testlibDB.SetupDBEnv(t)
	testDB := db.DB{DbMap: dbEnv.DbMap}
	defer testDB.Close()
	defer dbEnv.Close()

	// Create dependency tables
	if err := testDB.CreateTable(
		testDB.AddTable(nova.Hypervisor{}),
		testDB.AddTable(nova.Flavor{}),
	); err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	// Insert mock data into the hypervisors and flavors tables
	hs := []any{
		&nova.Hypervisor{ID: 1, Hostname: "hostname1", ServiceHost: "host1", FreeRAMMB: 16000, VCPUs: 16, VCPUsUsed: 4, FreeDiskGB: 200},
		&nova.Hypervisor{ID: 2, Hostname: "hostname2", ServiceHost: "host2", FreeRAMMB: 32000, VCPUs: 32, VCPUsUsed: 8, FreeDiskGB: 400},
	}
	if err := testDB.Insert(hs...); err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	fs := []any{
		&nova.Flavor{ID: "flavor1", RAM: 4000, VCPUs: 4, Disk: 50},
		&nova.Flavor{ID: "flavor2", RAM: 8000, VCPUs: 8, Disk: 100},
	}
	if err := testDB.Insert(fs...); err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	extractor := &FlavorHostSpaceExtractor{}
	if err := extractor.Init(testDB, conf.NewRawOpts("{}")); err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if _, err := extractor.Extract(); err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	// Verify the data was inserted into the feature_flavor_host_space table
	var spaces []FlavorHostSpace
	_, err := testDB.Select(&spaces, "SELECT * FROM feature_flavor_host_space")
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	if len(spaces) != 4 {
		t.Errorf("expected 4 rows, got %d", len(spaces))
	}

	expected := map[string]map[string]FlavorHostSpace{
		"host1": {
			"flavor1": {FlavorID: "flavor1", ComputeHost: "host1", RAMLeftMB: 12000, VCPUsLeft: 8, DiskLeftGB: 150},
			"flavor2": {FlavorID: "flavor2", ComputeHost: "host1", RAMLeftMB: 8000, VCPUsLeft: 4, DiskLeftGB: 100},
		},
		"host2": {
			"flavor1": {FlavorID: "flavor1", ComputeHost: "host2", RAMLeftMB: 28000, VCPUsLeft: 20, DiskLeftGB: 350},
			"flavor2": {FlavorID: "flavor2", ComputeHost: "host2", RAMLeftMB: 24000, VCPUsLeft: 16, DiskLeftGB: 300},
		},
	}

	for _, s := range spaces {
		exp := expected[s.ComputeHost][s.FlavorID]
		if exp.RAMLeftMB != s.RAMLeftMB {
			t.Errorf("expected ram_left for compute_host %s and flavor_id %s to be %d, got %d", s.ComputeHost, s.FlavorID, exp.RAMLeftMB, s.RAMLeftMB)
		}
		if exp.VCPUsLeft != s.VCPUsLeft {
			t.Errorf("expected cpu_left for compute_host %s and flavor_id %s to be %d, got %d", s.ComputeHost, s.FlavorID, exp.VCPUsLeft, s.VCPUsLeft)
		}
		if exp.DiskLeftGB != s.DiskLeftGB {
			t.Errorf("expected disk_left for compute_host %s and flavor_id %s to be %d, got %d", s.ComputeHost, s.FlavorID, exp.DiskLeftGB, s.DiskLeftGB)
		}
	}
}
