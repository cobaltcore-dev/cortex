// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package openstack

import (
	"context"
	"testing"

	"github.com/cobaltcore-dev/cortex/internal/db"
	"github.com/cobaltcore-dev/cortex/internal/sync"
	testlibDB "github.com/cobaltcore-dev/cortex/testlib/db"
	"github.com/cobaltcore-dev/cortex/testlib/mqtt"
)

type mockPlacementAPI struct{}

func (m *mockPlacementAPI) Init(ctx context.Context) {}

func (m *mockPlacementAPI) GetAllResourceProviders(ctx context.Context) ([]ResourceProvider, error) {
	return []ResourceProvider{{UUID: "1", Name: "rp1"}}, nil
}

func (m *mockPlacementAPI) GetAllTraits(ctx context.Context, rps []ResourceProvider) ([]Trait, error) {
	return []Trait{{ResourceProviderUUID: "1", Name: "trait1"}}, nil
}

func TestPlacementSyncer_Init(t *testing.T) {
	dbEnv := testlibDB.SetupDBEnv(t)
	testDB := db.DB{DbMap: dbEnv.DbMap}
	defer testDB.Close()
	defer dbEnv.Close()

	mon := sync.Monitor{}
	pc := &mockKeystoneAPI{}
	conf := PlacementConf{Types: []string{"resource_providers", "traits"}}

	syncer := &placementSyncer{
		db:   testDB,
		mon:  mon,
		conf: conf,
		api:  newPlacementAPI(mon, pc, conf),
	}
	syncer.Init(t.Context())
}

func TestPlacementSyncer_Sync(t *testing.T) {
	dbEnv := testlibDB.SetupDBEnv(t)
	testDB := db.DB{DbMap: dbEnv.DbMap}
	defer testDB.Close()
	defer dbEnv.Close()

	mon := sync.Monitor{}
	pc := &mockKeystoneAPI{}
	conf := PlacementConf{Types: []string{"resource_providers", "traits"}}

	syncer := &placementSyncer{
		db:         testDB,
		mon:        mon,
		conf:       conf,
		api:        newPlacementAPI(mon, pc, conf),
		mqttClient: &mqtt.MockClient{},
	}
	syncer.api = &mockPlacementAPI{}

	ctx := t.Context()
	err := syncer.Sync(ctx)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
}

func TestPlacementSyncer_SyncResourceProviders(t *testing.T) {
	dbEnv := testlibDB.SetupDBEnv(t)
	testDB := db.DB{DbMap: dbEnv.DbMap}
	defer testDB.Close()
	defer dbEnv.Close()

	mon := sync.Monitor{}
	pc := &mockKeystoneAPI{}
	conf := PlacementConf{Types: []string{"resource_providers", "traits"}}

	syncer := &placementSyncer{
		db:   testDB,
		mon:  mon,
		conf: conf,
		api:  newPlacementAPI(mon, pc, conf),
	}
	syncer.api = &mockPlacementAPI{}

	ctx := t.Context()
	rps, err := syncer.SyncResourceProviders(ctx)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if len(rps) != 1 {
		t.Fatalf("expected 1 resource provider, got %d", len(rps))
	}
}

func TestPlacementSyncer_SyncTraits(t *testing.T) {
	dbEnv := testlibDB.SetupDBEnv(t)
	testDB := db.DB{DbMap: dbEnv.DbMap}
	defer testDB.Close()
	defer dbEnv.Close()

	mon := sync.Monitor{}
	pc := &mockKeystoneAPI{}
	conf := PlacementConf{Types: []string{"resource_providers", "traits"}}

	syncer := &placementSyncer{
		db:   testDB,
		mon:  mon,
		conf: conf,
		api:  newPlacementAPI(mon, pc, conf),
	}
	syncer.api = &mockPlacementAPI{}

	ctx := t.Context()
	rps := []ResourceProvider{{UUID: "1", Name: "rp1"}}
	traits, err := syncer.SyncTraits(ctx, rps)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if len(traits) != 1 {
		t.Fatalf("expected 1 trait, got %d", len(traits))
	}
}
