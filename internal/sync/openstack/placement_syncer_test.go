// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package openstack

import (
	"errors"
	"testing"

	"github.com/cobaltcore-dev/cortex/internal/conf"
	"github.com/cobaltcore-dev/cortex/internal/sync"
	testlibDB "github.com/cobaltcore-dev/cortex/testlib/db"
)

type MockPlacementAPI struct {
	providers  []ResourceProvider
	traits     map[string][]ProviderDetail
	aggregates map[string][]ProviderDetail
}

func (m *MockPlacementAPI) ListResourceProviders(auth KeystoneAuth) ([]ResourceProvider, error) {
	return m.providers, nil
}

func (m *MockPlacementAPI) ResolveTraits(auth KeystoneAuth, provider ResourceProvider) ([]ProviderDetail, error) {
	if traits, ok := m.traits[provider.UUID]; ok {
		return traits, nil
	}
	return nil, errors.New("traits not found")
}

func (m *MockPlacementAPI) ResolveAggregates(auth KeystoneAuth, provider ResourceProvider) ([]ProviderDetail, error) {
	if aggregates, ok := m.aggregates[provider.UUID]; ok {
		return aggregates, nil
	}
	return nil, errors.New("aggregates not found")
}

func TestPlacementSyncer_Init(t *testing.T) {
	mockDB := testlibDB.NewMockDB()
	mockDB.Init()
	defer mockDB.Close()

	syncer := newPlacementSyncer(&mockDB, conf.SyncOpenStackConfig{}, sync.Monitor{}).(*placementSyncer)
	syncer.Init()

	// Check if the tables were created
	for _, model := range []any{(*ResourceProvider)(nil), (*ResourceProviderTrait)(nil), (*ResourceProviderAggregate)(nil)} {
		// Verify the table was created
		if _, err := mockDB.Get().Model(model).Exists(); err != nil {
			t.Fatalf("expected no error, got %v", err)
		}
	}
}

func TestPlacementSyncer_Sync(t *testing.T) {
	mockDB := testlibDB.NewMockDB()
	mockDB.Init()
	defer mockDB.Close()

	mockAPI := &MockPlacementAPI{
		providers: []ResourceProvider{
			{UUID: "provider1", Name: "Provider 1"},
			{UUID: "provider2", Name: "Provider 2"},
		},
		traits: map[string][]ProviderDetail{
			"provider1": {ResourceProviderTrait{ResourceProviderUUID: "provider1", Name: "trait1", ResourceProviderGeneration: 1}},
			"provider2": {ResourceProviderTrait{ResourceProviderUUID: "provider2", Name: "trait2", ResourceProviderGeneration: 1}},
		},
		aggregates: map[string][]ProviderDetail{
			"provider1": {ResourceProviderAggregate{ResourceProviderUUID: "provider1", UUID: "aggregate1", ResourceProviderGeneration: 1}},
			"provider2": {ResourceProviderAggregate{ResourceProviderUUID: "provider2", UUID: "aggregate2", ResourceProviderGeneration: 1}},
		},
	}

	syncer := &placementSyncer{
		Config:  conf.SyncOpenStackConfig{},
		API:     mockAPI,
		DB:      &mockDB,
		monitor: sync.Monitor{},
	}
	syncer.Init()

	auth := KeystoneAuth{token: "mock-token"}
	err := syncer.Sync(auth)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	// Check if the providers were inserted
	count, err := mockDB.Get().Model((*ResourceProvider)(nil)).Count()
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if count != 2 {
		t.Errorf("expected 2 providers, got %d", count)
	}

	// Check if the traits were inserted
	count, err = mockDB.Get().Model((*ResourceProviderTrait)(nil)).Count()
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if count != 2 {
		t.Errorf("expected 2 traits, got %d", count)
	}

	// Check if the aggregates were inserted
	count, err = mockDB.Get().Model((*ResourceProviderAggregate)(nil)).Count()
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if count != 2 {
		t.Errorf("expected 2 aggregates, got %d", count)
	}
}
