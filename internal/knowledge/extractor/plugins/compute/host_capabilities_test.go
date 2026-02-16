// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package compute

import (
	"testing"

	"github.com/cobaltcore-dev/cortex/api/v1alpha1"
	"github.com/cobaltcore-dev/cortex/internal/knowledge/datasources/plugins/openstack/nova"
	"github.com/cobaltcore-dev/cortex/internal/knowledge/datasources/plugins/openstack/placement"
	"github.com/cobaltcore-dev/cortex/internal/knowledge/db"
	testlibDB "github.com/cobaltcore-dev/cortex/internal/knowledge/db/testing"
)

func TestHostCapabilitiesExtractor_Init(t *testing.T) {
	extractor := &HostCapabilitiesExtractor{}
	config := v1alpha1.KnowledgeSpec{}
	if err := extractor.Init(nil, nil, config); err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
}

func TestHostCapabilitiesExtractor_Extract(t *testing.T) {
	dbEnv := testlibDB.SetupDBEnv(t)
	testDB := db.DB{DbMap: dbEnv.DbMap}
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

	extractor := &HostCapabilitiesExtractor{}
	config := v1alpha1.KnowledgeSpec{}
	if err := extractor.Init(&testDB, nil, config); err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	features, err := extractor.Extract()
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	if len(features) != 2 {
		t.Errorf("expected 2 rows, got %d", len(features))
	}

	// Compare expected values with actual values in features
	expected := []HostCapabilities{
		{
			ComputeHost: "host1",
			Traits:      "TRAIT_1,TRAIT_2",
		},
		{
			ComputeHost: "host2",
			Traits:      "TRAIT_3",
		},
	}

	for i, f := range features {
		hc := f.(HostCapabilities)
		if hc != expected[i] {
			t.Errorf("expected %+v, got %+v", expected[i], hc)
		}
	}
}
