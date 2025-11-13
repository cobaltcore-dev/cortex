// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package placement

import (
	"context"
	"testing"

	"github.com/cobaltcore-dev/cortex/knowledge/api/datasources/openstack/placement"
	"github.com/cobaltcore-dev/cortex/knowledge/api/v1alpha1"
	"github.com/cobaltcore-dev/cortex/knowledge/internal/datasources"
	"github.com/cobaltcore-dev/cortex/pkg/db"
	testlibDB "github.com/cobaltcore-dev/cortex/pkg/db/testing"
	testlibKeystone "github.com/cobaltcore-dev/cortex/pkg/keystone/testing"
)

type mockPlacementAPI struct{}

func (m *mockPlacementAPI) Init(ctx context.Context) error { return nil }

func (m *mockPlacementAPI) GetAllResourceProviders(ctx context.Context) ([]placement.ResourceProvider, error) {
	return []placement.ResourceProvider{{UUID: "1", Name: "rp1"}}, nil
}

func (m *mockPlacementAPI) GetAllTraits(ctx context.Context, rps []placement.ResourceProvider) ([]placement.Trait, error) {
	return []placement.Trait{{ResourceProviderUUID: "1", Name: "trait1"}}, nil
}

func (m *mockPlacementAPI) GetAllInventoryUsages(ctx context.Context, providers []placement.ResourceProvider) ([]placement.InventoryUsage, error) {
	return []placement.InventoryUsage{{ResourceProviderUUID: "1", InventoryClassName: "vcpu"}}, nil
}

func TestPlacementSyncer_Init(t *testing.T) {
	dbEnv := testlibDB.SetupDBEnv(t)
	testDB := db.DB{DbMap: dbEnv.DbMap}
	defer dbEnv.Close()
	mon := datasources.Monitor{}
	pc := &testlibKeystone.MockKeystoneAPI{}
	conf := v1alpha1.PlacementDatasource{Type: v1alpha1.PlacementDatasourceTypeResourceProviders}

	syncer := &PlacementSyncer{
		DB:   testDB,
		Mon:  mon,
		Conf: conf,
		API:  NewPlacementAPI(mon, pc, conf),
	}
	err := syncer.Init(t.Context())
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
}

func TestPlacementSyncer_Sync(t *testing.T) {
	dbEnv := testlibDB.SetupDBEnv(t)
	testDB := db.DB{DbMap: dbEnv.DbMap}
	defer dbEnv.Close()
	mon := datasources.Monitor{}
	pc := &testlibKeystone.MockKeystoneAPI{}
	conf := v1alpha1.PlacementDatasource{Type: v1alpha1.PlacementDatasourceTypeResourceProviders}

	syncer := &PlacementSyncer{
		DB:   testDB,
		Mon:  mon,
		Conf: conf,
		API:  NewPlacementAPI(mon, pc, conf),
	}
	syncer.API = &mockPlacementAPI{}

	ctx := t.Context()
	_, err := syncer.Sync(ctx)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
}

func TestPlacementSyncer_SyncResourceProviders(t *testing.T) {
	dbEnv := testlibDB.SetupDBEnv(t)
	testDB := db.DB{DbMap: dbEnv.DbMap}
	defer dbEnv.Close()
	mon := datasources.Monitor{}
	pc := &testlibKeystone.MockKeystoneAPI{}
	conf := v1alpha1.PlacementDatasource{Type: v1alpha1.PlacementDatasourceTypeResourceProviders}

	syncer := &PlacementSyncer{
		DB:   testDB,
		Mon:  mon,
		Conf: conf,
		API:  NewPlacementAPI(mon, pc, conf),
	}
	syncer.API = &mockPlacementAPI{}

	ctx := t.Context()
	n, err := syncer.SyncResourceProviders(ctx)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if n != 1 {
		t.Fatalf("expected 1 resource provider, got %d", n)
	}
}

func TestPlacementSyncer_SyncTraits(t *testing.T) {
	dbEnv := testlibDB.SetupDBEnv(t)
	testDB := db.DB{DbMap: dbEnv.DbMap}
	defer dbEnv.Close()
	mon := datasources.Monitor{}
	pc := &testlibKeystone.MockKeystoneAPI{}
	conf := v1alpha1.PlacementDatasource{Type: v1alpha1.PlacementDatasourceTypeResourceProviderTraits}

	rps := []placement.ResourceProvider{{UUID: "1", Name: "rp1"}}
	if err := testDB.CreateTable(testDB.AddTable(placement.ResourceProvider{})); err != nil {
		t.Fatalf("failed to create resource provider table: %v", err)
	}
	err := db.ReplaceAll(testDB, rps...)
	if err != nil {
		t.Fatalf("failed to insert resource providers: %v", err)
	}

	syncer := &PlacementSyncer{
		DB:   testDB,
		Mon:  mon,
		Conf: conf,
		API:  NewPlacementAPI(mon, pc, conf),
	}
	syncer.API = &mockPlacementAPI{}

	ctx := t.Context()
	// First, we need to sync resource providers to have them in the DB.
	n, err := syncer.SyncTraits(ctx)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if n != 1 {
		t.Fatalf("expected 1 trait, got %d", n)
	}
}

func TestPlacementSyncer_SyncInventoryUsages(t *testing.T) {
	dbEnv := testlibDB.SetupDBEnv(t)
	testDB := db.DB{DbMap: dbEnv.DbMap}
	defer dbEnv.Close()
	mon := datasources.Monitor{}
	pc := &testlibKeystone.MockKeystoneAPI{}
	conf := v1alpha1.PlacementDatasource{Type: v1alpha1.PlacementDatasourceTypeResourceProviderInventoryUsages}

	rps := []placement.ResourceProvider{{UUID: "1", Name: "rp1"}}
	if err := testDB.CreateTable(testDB.AddTable(placement.ResourceProvider{})); err != nil {
		t.Fatalf("failed to create resource provider table: %v", err)
	}
	err := db.ReplaceAll(testDB, rps...)
	if err != nil {
		t.Fatalf("failed to insert resource providers: %v", err)
	}

	syncer := &PlacementSyncer{
		DB:   testDB,
		Mon:  mon,
		Conf: conf,
		API:  NewPlacementAPI(mon, pc, conf),
	}
	syncer.API = &mockPlacementAPI{}

	ctx := t.Context()
	n, err := syncer.SyncInventoryUsages(ctx)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if n != 1 {
		t.Fatalf("expected 1 inventory usage, got %d", n)
	}
}
