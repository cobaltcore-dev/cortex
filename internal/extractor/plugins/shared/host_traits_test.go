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

func TestHostTraitsExtractor_Init(t *testing.T) {
	dbEnv := testlibDB.SetupDBEnv(t)
	testDB := db.DB{DbMap: dbEnv.DbMap}
	defer testDB.Close()
	defer dbEnv.Close()

	extractor := &HostTraitsExtractor{}
	config := conf.FeatureExtractorConfig{
		Name:           "host_traits_extractor",
		Options:        conf.NewRawOpts("{}"),
		RecencySeconds: nil, // No recency for this test
	}
	if err := extractor.Init(testDB, config); err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	if !testDB.TableExists(HostTraits{}) {
		t.Error("expected table to be created")
	}
}

func TestHostTraitsExtractor_Extract(t *testing.T) {
	dbEnv := testlibDB.SetupDBEnv(t)
	testDB := db.DB{DbMap: dbEnv.DbMap}
	defer testDB.Close()
	defer dbEnv.Close()

	// Create dependency tables
	if err := testDB.CreateTable(
		testDB.AddTable(nova.Hypervisor{}),
		testDB.AddTable(placement.Trait{}),
	); err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	// Insert mock data into the hypervisors and traits tables
	hypervisors := []any{
		&nova.Hypervisor{ID: "uuid1", ServiceHost: "host1"},
		&nova.Hypervisor{ID: "uuid2", ServiceHost: "host2"},
	}
	traits := []any{
		&placement.Trait{ResourceProviderUUID: "uuid1", Name: "TRAIT_1"},
		&placement.Trait{ResourceProviderUUID: "uuid1", Name: "TRAIT_2"},
		&placement.Trait{ResourceProviderUUID: "uuid2", Name: "TRAIT_3"},
	}
	if err := testDB.Insert(hypervisors...); err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if err := testDB.Insert(traits...); err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	extractor := &HostTraitsExtractor{}
	config := conf.FeatureExtractorConfig{
		Name:           "host_traits_extractor",
		Options:        conf.NewRawOpts("{}"),
		RecencySeconds: nil, // No recency for this test
	}
	if err := extractor.Init(testDB, config); err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if _, err := extractor.Extract(); err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	// Verify the data was inserted into the feature_host_traits table
	var traitsResult []HostTraits
	_, err := testDB.Select(&traitsResult, "SELECT * FROM feature_host_traits")
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	if len(traitsResult) != 2 {
		t.Errorf("expected 2 rows, got %d", len(traitsResult))
	}

	// Compare expected values with actual values in traitsResult
	expected := []HostTraits{
		{
			ComputeHost: "host1",
			Traits:      "TRAIT_1,TRAIT_2",
		},
		{
			ComputeHost: "host2",
			Traits:      "TRAIT_3",
		},
	}

	for i, trait := range traitsResult {
		if trait != expected[i] {
			t.Errorf("expected %+v, got %+v", expected[i], trait)
		}
	}
}
