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

func TestHostUtilizationExtractor_Extract_NoPlacementReservedCapacity(t *testing.T) {
	dbEnv := testlibDB.SetupDBEnv(t)
	testDB := db.DB{DbMap: dbEnv.DbMap}
	defer testDB.Close()
	defer dbEnv.Close()

	// Create dependency tables
	if err := testDB.CreateTable(
		testDB.AddTable(nova.Hypervisor{}),
		testDB.AddTable(placement.InventoryUsage{}),
	); err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	// Insert mock data into the hypervisors and flavors tables
	hs := []any{
		&nova.Hypervisor{ID: "1", Hostname: "hostname1", ServiceHost: "host1"},
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
			Reserved:             0,
		},
		&placement.InventoryUsage{
			ResourceProviderUUID: "1",
			InventoryClassName:   "VCPU",
			AllocationRatio:      1.0,
			Total:                100,
			Used:                 25,
			Reserved:             0,
		},
		&placement.InventoryUsage{
			ResourceProviderUUID: "1",
			InventoryClassName:   "DISK_GB",
			AllocationRatio:      1.0,
			Total:                2000,
			Used:                 1000,
			Reserved:             0,
		},
	}
	if err := testDB.Insert(is...); err != nil {
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
	table := HostUtilization{}.TableName()
	_, err := testDB.Select(&utilizations, "SELECT * FROM "+table)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	// Compare expected values with actual values in utilizations
	expected := []HostUtilization{
		{
			ComputeHost:            "host1",
			TotalVCPUsAllocatable:  100,
			TotalRAMAllocatableMB:  1000,
			TotalDiskAllocatableGB: 2000,
			VCPUsUsed:              25,
			RAMUsedMB:              500,
			DiskUsedGB:             1000,
			VCPUsUtilizedPct:       25,
			RAMUtilizedPct:         50,
			DiskUtilizedPct:        50,
		},
	}

	if len(utilizations) != len(expected) {
		t.Errorf("expected 1 row, got %d", len(utilizations))
	}

	for i, utilization := range utilizations {
		if utilization != expected[i] {
			t.Errorf("expected %+v, got %+v", expected[i], utilization)
		}
	}
}

func TestHostUtilizationExtractor_Extract_IgnoreNoUsageDataHosts(t *testing.T) {
	dbEnv := testlibDB.SetupDBEnv(t)
	testDB := db.DB{DbMap: dbEnv.DbMap}
	defer testDB.Close()
	defer dbEnv.Close()

	// Create dependency tables
	if err := testDB.CreateTable(
		testDB.AddTable(nova.Hypervisor{}),
		testDB.AddTable(placement.InventoryUsage{}),
	); err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	// Insert mock data into the hypervisors and flavors tables
	hs := []any{
		&nova.Hypervisor{ID: "1", Hostname: "hostname1", ServiceHost: "host1"},
		// No usage data for this host
		&nova.Hypervisor{ID: "2", Hostname: "hostname2", ServiceHost: "host2"},
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
			Reserved:             0,
		},
		&placement.InventoryUsage{
			ResourceProviderUUID: "1",
			InventoryClassName:   "VCPU",
			AllocationRatio:      1.0,
			Total:                100,
			Used:                 25,
			Reserved:             0,
		},
		&placement.InventoryUsage{
			ResourceProviderUUID: "1",
			InventoryClassName:   "DISK_GB",
			AllocationRatio:      1.0,
			Total:                2000,
			Used:                 1000,
			Reserved:             0,
		},
	}
	if err := testDB.Insert(is...); err != nil {
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
	table := HostUtilization{}.TableName()
	_, err := testDB.Select(&utilizations, "SELECT * FROM "+table)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	// Only one host has usage data
	if len(utilizations) > 1 {
		t.Errorf("expected 1 row, got %d", len(utilizations))
	}

	if utilizations[0].ComputeHost != "host1" {
		t.Errorf("expected host1, got %s", utilizations[0].ComputeHost)
	}
}

func TestHostUtilizationExtractor_Extract_WithPlacementReservedCapacity(t *testing.T) {
	dbEnv := testlibDB.SetupDBEnv(t)
	testDB := db.DB{DbMap: dbEnv.DbMap}
	defer testDB.Close()
	defer dbEnv.Close()

	// Create dependency tables
	if err := testDB.CreateTable(
		testDB.AddTable(nova.Hypervisor{}),
		testDB.AddTable(placement.InventoryUsage{}),
	); err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	// Insert mock data into the hypervisors and flavors tables
	hs := []any{
		&nova.Hypervisor{ID: "1", Hostname: "hostname1", ServiceHost: "host1"},
		// No usage data for this host
		&nova.Hypervisor{ID: "2", Hostname: "hostname2", ServiceHost: "host2"},
	}
	if err := testDB.Insert(hs...); err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	is := []any{
		&placement.InventoryUsage{
			ResourceProviderUUID: "1",
			InventoryClassName:   "MEMORY_MB",
			AllocationRatio:      1.0,
			Total:                1100,
			Used:                 500,
			Reserved:             100,
		},
		&placement.InventoryUsage{
			ResourceProviderUUID: "1",
			InventoryClassName:   "VCPU",
			AllocationRatio:      1.0,
			Total:                105,
			Used:                 25,
			Reserved:             5,
		},
		&placement.InventoryUsage{
			ResourceProviderUUID: "1",
			InventoryClassName:   "DISK_GB",
			AllocationRatio:      1.0,
			Total:                2100,
			Used:                 1000,
			Reserved:             100,
		},
	}
	if err := testDB.Insert(is...); err != nil {
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
	table := HostUtilization{}.TableName()
	_, err := testDB.Select(&utilizations, "SELECT * FROM "+table)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	// Compare expected values with actual values in utilizations
	expected := []HostUtilization{
		{
			ComputeHost:            "host1",
			TotalVCPUsAllocatable:  100,
			TotalRAMAllocatableMB:  1000,
			TotalDiskAllocatableGB: 2000,
			VCPUsUsed:              25,
			RAMUsedMB:              500,
			DiskUsedGB:             1000,
			VCPUsUtilizedPct:       25,
			RAMUtilizedPct:         50,
			DiskUtilizedPct:        50,
		},
	}

	if len(utilizations) != len(expected) {
		t.Errorf("expected 1 row, got %d", len(utilizations))
	}

	for i, utilization := range utilizations {
		if utilization != expected[i] {
			t.Errorf("expected %+v, got %+v", expected[i], utilization)
		}
	}
}

func TestHostUtilizationExtractor_Extract_WithOvercommit(t *testing.T) {
	dbEnv := testlibDB.SetupDBEnv(t)
	testDB := db.DB{DbMap: dbEnv.DbMap}
	defer testDB.Close()
	defer dbEnv.Close()

	// Create dependency tables
	if err := testDB.CreateTable(
		testDB.AddTable(nova.Hypervisor{}),
		testDB.AddTable(placement.InventoryUsage{}),
	); err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	// Insert mock data into the hypervisors and flavors tables
	hs := []any{
		&nova.Hypervisor{ID: "1", Hostname: "hostname1", ServiceHost: "host1"},
		// No usage data for this host
		&nova.Hypervisor{ID: "2", Hostname: "hostname2", ServiceHost: "host2"},
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
			Reserved:             0,
		},
		// Expect total allocatable vcpus to be 200 [(110-10)*2]
		&placement.InventoryUsage{
			ResourceProviderUUID: "1",
			InventoryClassName:   "VCPU",
			AllocationRatio:      2.0,
			Total:                110, // Does not include overcommit
			Used:                 50,  // Already includes overcommit
			Reserved:             10,  // Does not include overcommit
		},
		&placement.InventoryUsage{
			ResourceProviderUUID: "1",
			InventoryClassName:   "DISK_GB",
			AllocationRatio:      1.0,
			Total:                2000,
			Used:                 1000,
			Reserved:             0,
		},
	}
	if err := testDB.Insert(is...); err != nil {
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
	table := HostUtilization{}.TableName()
	_, err := testDB.Select(&utilizations, "SELECT * FROM "+table)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	// Compare expected values with actual values in utilizations
	expected := []HostUtilization{
		{
			ComputeHost:            "host1",
			TotalVCPUsAllocatable:  200,
			TotalRAMAllocatableMB:  1000,
			TotalDiskAllocatableGB: 2000,
			VCPUsUsed:              50,
			RAMUsedMB:              500,
			DiskUsedGB:             1000,
			VCPUsUtilizedPct:       25,
			RAMUtilizedPct:         50,
			DiskUtilizedPct:        50,
		},
	}

	if len(utilizations) != len(expected) {
		t.Errorf("expected 1 row, got %d", len(utilizations))
	}

	for i, utilization := range utilizations {
		if utilization != expected[i] {
			t.Errorf("expected %+v, got %+v", expected[i], utilization)
		}
	}
}
