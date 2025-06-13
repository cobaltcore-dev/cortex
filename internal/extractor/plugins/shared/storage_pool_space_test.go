// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package shared

import (
	"testing"

	"github.com/cobaltcore-dev/cortex/internal/conf"
	"github.com/cobaltcore-dev/cortex/internal/db"
	testlibDB "github.com/cobaltcore-dev/cortex/testlib/db"
)

func TestStoragePoolSpaceExtractor_Init(t *testing.T) {
	dbEnv := testlibDB.SetupDBEnv(t)
	testDB := db.DB{DbMap: dbEnv.DbMap}
	defer testDB.Close()
	defer dbEnv.Close()

	extractor := &StoragePoolSpaceExtractor{}
	config := conf.FeatureExtractorConfig{
		Name:           "storage_pool_space_extractor",
		Options:        conf.NewRawOpts("{}"),
		RecencySeconds: nil, // No recency for this test
	}
	if err := extractor.Init(testDB, config); err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	if !testDB.TableExists(StoragePoolSpace{}) {
		t.Error("expected table to be created")
	}
}

func TestStoragePoolSpaceExtractor_Extract(t *testing.T) {
	dbEnv := testlibDB.SetupDBEnv(t)
	testDB := db.DB{DbMap: dbEnv.DbMap}
	defer testDB.Close()
	defer dbEnv.Close()

	// Create dependency table
	_, err := testDB.Exec(`
		CREATE TABLE openstack_storage_pools (
			name TEXT PRIMARY KEY,
			capabilities_free_capacity_gb FLOAT,
			capabilities_total_capacity_gb FLOAT
		)`)
	if err != nil {
		t.Fatalf("failed to create openstack_storage_pools: %v", err)
	}

	// Insert mock data
	_, err = testDB.Exec(`
		INSERT INTO openstack_storage_pools (name, capabilities_free_capacity_gb, capabilities_total_capacity_gb) VALUES
		('pool1', 100, 200),
		('pool2', -10, 100),
		('pool3', 50, NULL),
		('pool4', 25, 0),
		('pool5', 30, 60)
	`)
	if err != nil {
		t.Fatalf("failed to insert mock data: %v", err)
	}

	extractor := &StoragePoolSpaceExtractor{}
	config := conf.FeatureExtractorConfig{
		Name:           "storage_pool_space_extractor",
		Options:        conf.NewRawOpts("{}"),
		RecencySeconds: nil, // No recency for this test
	}
	if err := extractor.Init(testDB, config); err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if _, err := extractor.Extract(); err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	// Verify the data was inserted into the feature_storage_pool_space table
	var spaces []StoragePoolSpace
	_, err = testDB.Select(&spaces, "SELECT * FROM feature_storage_pool_space")
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	expected := []StoragePoolSpace{
		{StoragePoolName: "pool1", CapacityLeftGB: 100, CapacityLeftPct: 50.0},
		{StoragePoolName: "pool2", CapacityLeftGB: 0, CapacityLeftPct: 0},
		{StoragePoolName: "pool3", CapacityLeftGB: 50, CapacityLeftPct: 0},
		{StoragePoolName: "pool4", CapacityLeftGB: 25, CapacityLeftPct: 0},
		{StoragePoolName: "pool5", CapacityLeftGB: 30, CapacityLeftPct: 50.0},
	}

	if len(spaces) != len(expected) {
		t.Errorf("expected %d rows, got %d", len(expected), len(spaces))
	}

	for i, space := range spaces {
		if space != expected[i] {
			t.Errorf("expected %+v, got %+v", expected[i], space)
		}
	}
}
