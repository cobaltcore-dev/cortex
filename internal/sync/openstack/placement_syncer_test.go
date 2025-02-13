// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package openstack

import (
	"errors"
	"testing"

	"github.com/cobaltcore-dev/cortex/internal/conf"
	"github.com/cobaltcore-dev/cortex/internal/db"
	"github.com/cobaltcore-dev/cortex/internal/sync"
	testlibDB "github.com/cobaltcore-dev/cortex/testlib/db"
)

type MockPlacementAPI struct {
	providers  []ResourceProvider
	traits     map[string][]ResourceProviderTrait
	aggregates map[string][]ResourceProviderAggregate
}

func (m *MockPlacementAPI) ListResourceProviders(auth KeystoneAuth) ([]ResourceProvider, error) {
	return m.providers, nil
}

func (m *MockPlacementAPI) ResolveTraits(auth KeystoneAuth, provider ResourceProvider) ([]ResourceProviderTrait, error) {
	if traits, ok := m.traits[provider.UUID]; ok {
		return traits, nil
	}
	return nil, errors.New("traits not found")
}

func (m *MockPlacementAPI) ResolveAggregates(auth KeystoneAuth, provider ResourceProvider) ([]ResourceProviderAggregate, error) {
	if aggregates, ok := m.aggregates[provider.UUID]; ok {
		return aggregates, nil
	}
	return nil, errors.New("aggregates not found")
}

func TestPlacementSyncer_Init(t *testing.T) {
	testDB := testlibDB.NewSqliteTestDB(t)
	defer testDB.Close()

	mon := sync.Monitor{}
	conf := conf.SyncOpenStackConfig{}
	syncer := &placementSyncer{
		Config:        conf,
		API:           NewPlacementAPI(conf, mon),
		DB:            *testDB.DB,
		monitor:       mon,
		sleepInterval: 0,
	}
	syncer.Init()

	// Check if the tables were created
	for _, model := range []db.Table{
		ResourceProvider{},
		ResourceProviderTrait{},
		ResourceProviderAggregate{},
	} {
		if !testDB.TableExists(model) {
			t.Error("expected table to be created")
		}
	}
}

func TestPlacementSyncer_Sync(t *testing.T) {
	testDB := testlibDB.NewSqliteTestDB(t)
	defer testDB.Close()

	mockAPI := &MockPlacementAPI{
		providers: []ResourceProvider{
			{UUID: "provider1", Name: "Provider 1"},
			{UUID: "provider2", Name: "Provider 2"},
		},
		traits: map[string][]ResourceProviderTrait{
			"provider1": {{ResourceProviderUUID: "provider1", Name: "trait1", ResourceProviderGeneration: 1}},
			"provider2": {{ResourceProviderUUID: "provider2", Name: "trait2", ResourceProviderGeneration: 1}},
		},
		aggregates: map[string][]ResourceProviderAggregate{
			"provider1": {{ResourceProviderUUID: "provider1", UUID: "aggregate1", ResourceProviderGeneration: 1}},
			"provider2": {{ResourceProviderUUID: "provider2", UUID: "aggregate2", ResourceProviderGeneration: 1}},
		},
	}

	syncer := &placementSyncer{
		Config:  conf.SyncOpenStackConfig{},
		API:     mockAPI,
		DB:      *testDB.DB,
		monitor: sync.Monitor{},
	}
	syncer.Init()

	auth := KeystoneAuth{token: "mock-token"}
	err := syncer.Sync(auth)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	// Check if the providers were inserted
	var count int
	err = testDB.SelectOne(&count, "SELECT COUNT(*) FROM "+ResourceProvider{}.TableName())
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if count != 2 {
		t.Errorf("expected 2 providers, got %d", count)
	}

	// Check if the traits were inserted
	err = testDB.SelectOne(&count, "SELECT COUNT(*) FROM "+ResourceProviderTrait{}.TableName())
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if count != 2 {
		t.Errorf("expected 2 traits, got %d", count)
	}

	// Check if the aggregates were inserted
	err = testDB.SelectOne(&count, "SELECT COUNT(*) FROM "+ResourceProviderAggregate{}.TableName())
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if count != 2 {
		t.Errorf("expected 2 aggregates, got %d", count)
	}
}
