// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package shared

import (
	"testing"

	"github.com/cobaltcore-dev/cortex/internal/conf"
	"github.com/cobaltcore-dev/cortex/internal/db"
	"github.com/cobaltcore-dev/cortex/internal/sync/openstack/manila"
	testlibDB "github.com/cobaltcore-dev/cortex/testlib/db"
)

func TestStoragePoolUtilizationExtractor_Init(t *testing.T) {
	dbEnv := testlibDB.SetupDBEnv(t)
	testDB := db.DB{DbMap: dbEnv.DbMap}
	defer testDB.Close()
	defer dbEnv.Close()

	extractor := &StoragePoolUtilizationExtractor{}
	config := conf.FeatureExtractorConfig{
		Name:           "storage_pool_utilization_extractor",
		Options:        conf.NewRawOpts("{}"),
		RecencySeconds: nil, // No recency for this test
	}
	if err := extractor.Init(testDB, config); err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	if !testDB.TableExists(StoragePoolUtilization{}) {
		t.Error("expected table to be created")
	}
}

func TestStoragePoolUtilizationExtractor_Extract(t *testing.T) {
	dbEnv := testlibDB.SetupDBEnv(t)
	testDB := db.DB{DbMap: dbEnv.DbMap}
	defer testDB.Close()
	defer dbEnv.Close()

	// Create dependency tables
	if err := testDB.CreateTable(
		testDB.AddTable(manila.StoragePool{}),
	); err != nil {
		t.Fatalf("failed to create dependency tables: %v", err)
	}

	// Insert mock data
	_, err := testDB.Exec(`
		INSERT INTO openstack_storage_pools (name, capabilities_reserved_percentage) VALUES
		('pool1', 75),
		('pool2', 100),
		('pool3', NULL),
		('pool4', 0),
		('pool5', 60)
	`)
	if err != nil {
		t.Fatalf("failed to insert mock data: %v", err)
	}

	extractor := &StoragePoolUtilizationExtractor{}
	config := conf.FeatureExtractorConfig{
		Name:           "storage_pool_utilization_extractor",
		Options:        conf.NewRawOpts("{}"),
		RecencySeconds: nil, // No recency for this test
	}
	if err := extractor.Init(testDB, config); err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if _, err := extractor.Extract(); err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	// Verify the data was inserted into the feature_storage_pool_utilization table
	var utilizations []StoragePoolUtilization
	_, err = testDB.Select(&utilizations, "SELECT * FROM feature_storage_pool_utilization")
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	expected := []StoragePoolUtilization{
		{StoragePoolName: "pool1", CapacityUtilizedPct: 75.0},
		{StoragePoolName: "pool2", CapacityUtilizedPct: 100.0},
		{StoragePoolName: "pool3", CapacityUtilizedPct: 0},
		{StoragePoolName: "pool4", CapacityUtilizedPct: 0},
		{StoragePoolName: "pool5", CapacityUtilizedPct: 60.0},
	}

	if len(utilizations) != len(expected) {
		t.Errorf("expected %d rows, got %d", len(expected), len(utilizations))
	}

	for i, utilization := range utilizations {
		if utilization != expected[i] {
			t.Errorf("expected %+v, got %+v", expected[i], utilization)
		}
	}
}
