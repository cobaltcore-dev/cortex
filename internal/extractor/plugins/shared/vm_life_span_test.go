// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package shared

import (
	"os"
	"testing"

	"github.com/cobaltcore-dev/cortex/internal/conf"
	"github.com/cobaltcore-dev/cortex/internal/db"
	"github.com/cobaltcore-dev/cortex/internal/sync/openstack/nova"
	testlibDB "github.com/cobaltcore-dev/cortex/testlib/db"
)

func TestVMLifeSpanExtractor_Init(t *testing.T) {
	dbEnv := testlibDB.SetupDBEnv(t)
	testDB := db.DB{DbMap: dbEnv.DbMap}
	defer testDB.Close()
	defer dbEnv.Close()

	extractor := &VMLifeSpanExtractor{}
	config := conf.FeatureExtractorConfig{
		Name:           "vm_life_span_extractor",
		Options:        conf.NewRawOpts("{}"),
		RecencySeconds: nil, // No recency for this test
	}
	if err := extractor.Init(testDB, config); err != nil {
		t.Fatalf("expected no error during initialization, got %v", err)
	}

	if !testDB.TableExists(VMLifeSpan{}) {
		t.Error("expected table to be created")
	}
}

func TestVMLifeSpanExtractor_Extract(t *testing.T) {
	// We're using postgres specific syntax here.
	if os.Getenv("POSTGRES_CONTAINER") != "1" {
		t.Skip("skipping test; set POSTGRES_CONTAINER=1 to run")
	}

	dbEnv := testlibDB.SetupDBEnv(t)
	testDB := db.DB{DbMap: dbEnv.DbMap}
	defer testDB.Close()
	defer dbEnv.Close()

	// Create dependency tables
	if err := testDB.CreateTable(
		testDB.AddTable(nova.Server{}),
		testDB.AddTable(nova.Flavor{}),
	); err != nil {
		t.Fatalf("failed to create dependency tables: %v", err)
	}

	// Insert mock data into the servers and flavors tables
	if _, err := testDB.Exec(`
        INSERT INTO openstack_servers (id, flavor_id, created, updated, status)
        VALUES
            ('server1', 'flavor1', '2025-01-01T00:00:00Z', '2025-01-03T00:00:00Z', 'DELETED'),
            ('server2', 'flavor2', '2025-01-02T00:00:00Z', '2025-01-04T00:00:00Z', 'DELETED')
    `); err != nil {
		t.Fatalf("failed to insert servers: %v", err)
	}

	flavors := []any{
		&nova.Flavor{ID: "flavor1", Name: "small"},
		&nova.Flavor{ID: "flavor2", Name: "medium"},
	}
	if err := testDB.Insert(flavors...); err != nil {
		t.Fatalf("failed to insert flavors: %v", err)
	}

	extractor := &VMLifeSpanExtractor{}
	config := conf.FeatureExtractorConfig{
		Name:           "vm_life_span_extractor",
		Options:        conf.NewRawOpts("{}"),
		RecencySeconds: nil, // No recency for this test
	}
	if err := extractor.Init(testDB, config); err != nil {
		t.Fatalf("expected no error during initialization, got %v", err)
	}

	features, err := extractor.Extract()
	if err != nil {
		t.Fatalf("expected no error during extraction, got %v", err)
	}

	if len(features) != 2 {
		t.Errorf("expected 2 features, got %d", len(features))
	}

	expected := map[string]VMLifeSpan{
		"server1": {Duration: 172800, FlavorID: "flavor1", FlavorName: "small", InstanceUUID: "server1"},
		"server2": {Duration: 172800, FlavorID: "flavor2", FlavorName: "medium", InstanceUUID: "server2"},
	}

	for _, feature := range features {
		vmLifeSpan := feature.(VMLifeSpan)
		expectedFeature := expected[vmLifeSpan.InstanceUUID]

		if vmLifeSpan != expectedFeature {
			t.Errorf("unexpected feature for instance %s: got %+v, expected %+v", vmLifeSpan.InstanceUUID, vmLifeSpan, expectedFeature)
		}
	}
}
