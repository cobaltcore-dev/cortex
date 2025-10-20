// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package cinder

import (
	"context"
	"testing"

	"github.com/cobaltcore-dev/cortex/knowledge/api/datasources/openstack/cinder"
	"github.com/cobaltcore-dev/cortex/knowledge/api/v1alpha1"
	"github.com/cobaltcore-dev/cortex/knowledge/internal/datasources"
	"github.com/cobaltcore-dev/cortex/lib/db"
	testlibDB "github.com/cobaltcore-dev/cortex/testlib/db"
	"github.com/cobaltcore-dev/cortex/testlib/mqtt"
)

type mockCinderAPI struct{}

func (m *mockCinderAPI) Init(ctx context.Context) {}

func (m *mockCinderAPI) GetAllStoragePools(ctx context.Context) ([]cinder.StoragePool, error) {
	return []cinder.StoragePool{{Name: "pool1"}}, nil
}

func TestCinderSyncer_Init(t *testing.T) {
	dbEnv := testlibDB.SetupDBEnv(t)
	testDB := db.DB{DbMap: dbEnv.DbMap}
	defer testDB.Close()
	defer dbEnv.Close()

	mon := datasources.Monitor{}
	conf := v1alpha1.CinderDatasource{Types: []string{"storage_pools"}}

	syncer := &CinderSyncer{
		DB:   testDB,
		Mon:  mon,
		Conf: conf,
		API:  &mockCinderAPI{},
	}
	syncer.Init(t.Context())
}

func TestCinderSyncer_Sync(t *testing.T) {
	dbEnv := testlibDB.SetupDBEnv(t)
	testDB := db.DB{DbMap: dbEnv.DbMap}
	defer testDB.Close()
	defer dbEnv.Close()

	mon := datasources.Monitor{}
	conf := v1alpha1.CinderDatasource{Types: []string{"storage_pools"}}

	syncer := &CinderSyncer{
		DB:         testDB,
		Mon:        mon,
		Conf:       conf,
		API:        &mockCinderAPI{},
		MqttClient: &mqtt.MockClient{},
	}

	ctx := t.Context()
	err := syncer.Sync(ctx)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
}

func TestCinderSyncer_SyncAllStoragePools(t *testing.T) {
	dbEnv := testlibDB.SetupDBEnv(t)
	testDB := db.DB{DbMap: dbEnv.DbMap}
	defer testDB.Close()
	defer dbEnv.Close()

	mon := datasources.Monitor{}
	conf := v1alpha1.CinderDatasource{Types: []string{"storage_pools"}}

	syncer := &CinderSyncer{
		DB:   testDB,
		Mon:  mon,
		Conf: conf,
		API:  &mockCinderAPI{},
	}

	ctx := t.Context()
	pools, err := syncer.SyncAllStoragePools(ctx)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if len(pools) != 1 {
		t.Fatalf("expected 1 storage pool, got %d", len(pools))
	}
}
