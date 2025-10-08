// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package shared

import (
	"os"
	"testing"

	"github.com/cobaltcore-dev/cortex/extractor/api/features/shared"
	"github.com/cobaltcore-dev/cortex/extractor/internal/conf"
	libconf "github.com/cobaltcore-dev/cortex/internal/conf"
	"github.com/cobaltcore-dev/cortex/internal/db"
	"github.com/cobaltcore-dev/cortex/sync/api/objects/openstack/nova"
	testlibDB "github.com/cobaltcore-dev/cortex/testlib/db"
)

func TestVMHostResidencyExtractor_Init(t *testing.T) {
	dbEnv := testlibDB.SetupDBEnv(t)
	testDB := db.DB{DbMap: dbEnv.DbMap}
	defer testDB.Close()
	defer dbEnv.Close()

	extractor := &VMHostResidencyExtractor{}
	config := conf.FeatureExtractorConfig{
		Name:           "vm_host_residency_extractor",
		Options:        libconf.NewRawOpts("{}"),
		RecencySeconds: nil,
	}
	if err := extractor.Init(testDB, config); err != nil {
		t.Fatalf("expected no error during initialization, got %v", err)
	}

	if !testDB.TableExists(shared.VMHostResidency{}) {
		t.Error("expected table to be created")
	}
}

func TestVMHostResidencyExtractor_Extract(t *testing.T) {
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
		testDB.AddTable(nova.Migration{}),
		testDB.AddTable(nova.Flavor{}),
	); err != nil {
		t.Fatalf("failed to create dependency tables: %v", err)
	}

	servers := []any{
		&nova.Server{ID: "server1", FlavorName: "small", Created: "2025-01-01T00:00:00Z"},
		&nova.Server{ID: "server2", FlavorName: "medium", Created: "2025-01-02T00:00:00Z"},
	}
	if err := testDB.Insert(servers...); err != nil {
		t.Fatalf("failed to insert servers: %v", err)
	}

	migrations := []any{
		&nova.Migration{ID: 1, UUID: "migration1", InstanceUUID: "server1", SourceCompute: "host1", DestCompute: "host2", CreatedAt: "2025-01-03T00:00:00Z", MigrationType: "live-migration"},
		&nova.Migration{ID: 2, UUID: "migration2", InstanceUUID: "server2", SourceCompute: "host2", DestCompute: "host3", CreatedAt: "2025-01-04T00:00:00Z", MigrationType: "resize"},
	}
	if err := testDB.Insert(migrations...); err != nil {
		t.Fatalf("failed to insert migrations: %v", err)
	}

	flavors := []any{
		&nova.Flavor{ID: "flavor1", Name: "small"},
		&nova.Flavor{ID: "flavor2", Name: "medium"},
	}
	if err := testDB.Insert(flavors...); err != nil {
		t.Fatalf("failed to insert flavors: %v", err)
	}

	extractor := &VMHostResidencyExtractor{}
	config := conf.FeatureExtractorConfig{
		Name:           "vm_host_residency_extractor",
		Options:        libconf.NewRawOpts("{}"),
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

	expected := map[string]shared.VMHostResidency{
		"migration1": {Duration: 172800, FlavorName: "small", InstanceUUID: "server1", MigrationUUID: "migration1", SourceHost: "host1", TargetHost: "host2", Type: "live-migration", Time: 1735862400, ProjectID: "", UserID: ""},
		"migration2": {Duration: 172800, FlavorName: "medium", InstanceUUID: "server2", MigrationUUID: "migration2", SourceHost: "host2", TargetHost: "host3", Type: "resize", Time: 1735948800, ProjectID: "", UserID: ""},
	}

	for _, feature := range features {
		vmResidency := feature.(shared.VMHostResidency)
		expectedFeature := expected[vmResidency.MigrationUUID]

		if vmResidency != expectedFeature {
			t.Errorf("unexpected feature for migration %s: got %+v, expected %+v", vmResidency.MigrationUUID, vmResidency, expectedFeature)
		}
	}
}
