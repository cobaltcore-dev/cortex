// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package shared

import (
	"testing"

	"github.com/cobaltcore-dev/cortex/internal/conf"
	"github.com/cobaltcore-dev/cortex/internal/db"
	"github.com/cobaltcore-dev/cortex/internal/sync/openstack/nova"
	"github.com/cobaltcore-dev/cortex/internal/sync/openstack/placement"
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
		testDB.AddTable(placement.InventoryUsage{}),
		testDB.AddTable(nova.Aggregate{}),
	); err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	// Insert mock data into the hypervisors and flavors tables
	hs := []any{
		&nova.Hypervisor{ID: "1", Hostname: "hostname1", ServiceHost: "host1"},
		&nova.Hypervisor{ID: "2", Hostname: "hostname2", ServiceHost: "host2"},
		// No utilization data for this host
		&nova.Hypervisor{ID: "3", Hostname: "hostname3", ServiceHost: "host3"},
	}
	if err := testDB.Insert(hs...); err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	is := []any{
		&placement.InventoryUsage{
			ResourceProviderUUID: "1",
			InventoryClassName:   "MEMORY_MB",
			AllocationRatio:      1.0,
			Total:                1000,
			Used:                 500,
		},
		&placement.InventoryUsage{
			ResourceProviderUUID: "1",
			InventoryClassName:   "VCPU",
			AllocationRatio:      1.0,
			Total:                100,
			Used:                 25,
		},
		&placement.InventoryUsage{
			ResourceProviderUUID: "1",
			InventoryClassName:   "DISK_GB",
			AllocationRatio:      1.0,
			Total:                2000,
			Used:                 1000,
		},
		&placement.InventoryUsage{
			ResourceProviderUUID: "2",
			InventoryClassName:   "MEMORY_MB",
			AllocationRatio:      2.0,
			Total:                1000,
			Used:                 500,
		},
		&placement.InventoryUsage{
			ResourceProviderUUID: "2",
			InventoryClassName:   "VCPU",
			AllocationRatio:      2.0,
			Total:                100,
			Used:                 25,
		},
		&placement.InventoryUsage{
			ResourceProviderUUID: "2",
			InventoryClassName:   "DISK_GB",
			AllocationRatio:      2.0,
			Total:                2000,
			Used:                 1000,
		},
	}
	if err := testDB.Insert(is...); err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	availabilityZone := "zone1"
	as := []any{
		&nova.Aggregate{Name: "aggregate1", AvailabilityZone: &availabilityZone, ComputeHost: "host1"},
		&nova.Aggregate{Name: "aggregate2", AvailabilityZone: &availabilityZone, ComputeHost: "host2"},
	}
	if err := testDB.Insert(as...); err != nil {
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
			ComputeHost:              "host1",
			RAMUtilizedPct:           50,
			TotalMemoryAllocatableMB: 1000,
			VCPUsUtilizedPct:         25,
			TotalVCPUsAllocatable:    100,
			DiskUtilizedPct:          50,
			TotalDiskAllocatableGB:   2000,
			AvailabilityZone:         availabilityZone,
		},
		{
			ComputeHost:              "host2",
			RAMUtilizedPct:           25,
			TotalMemoryAllocatableMB: 2000,
			VCPUsUtilizedPct:         12.5,
			TotalVCPUsAllocatable:    200,
			DiskUtilizedPct:          25,
			TotalDiskAllocatableGB:   4000,
			AvailabilityZone:         availabilityZone,
		},
	}

	for i, utilization := range utilizations {
		if utilization != expected[i] {
			t.Errorf("expected %+v, got %+v", expected[i], utilization)
		}
	}
}
