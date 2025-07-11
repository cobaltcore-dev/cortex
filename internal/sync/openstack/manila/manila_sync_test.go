// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package manila

import (
	"context"
	"testing"

	"github.com/cobaltcore-dev/cortex/internal/db"
	"github.com/cobaltcore-dev/cortex/internal/sync"
	testlibDB "github.com/cobaltcore-dev/cortex/testlib/db"
	"github.com/cobaltcore-dev/cortex/testlib/mqtt"
)

type mockManilaAPI struct{}

func (m *mockManilaAPI) Init(ctx context.Context) {}

func (m *mockManilaAPI) GetAllStoragePools(ctx context.Context) ([]StoragePool, error) {
	return []StoragePool{{Name: "pool1", Host: "host1", Backend: "backend1", Pool: "poolA"}}, nil
}

func TestManilaSyncer_Init(t *testing.T) {
	dbEnv := testlibDB.SetupDBEnv(t)
	testDB := db.DB{DbMap: dbEnv.DbMap}
	defer testDB.Close()
	defer dbEnv.Close()

	mon := sync.Monitor{}
	conf := ManilaConf{Types: []string{"storage_pools"}}

	syncer := &ManilaSyncer{
		DB:   testDB,
		Mon:  mon,
		Conf: conf,
		API:  &mockManilaAPI{},
	}
	syncer.Init(t.Context())
}

func TestManilaSyncer_Sync(t *testing.T) {
	dbEnv := testlibDB.SetupDBEnv(t)
	testDB := db.DB{DbMap: dbEnv.DbMap}
	defer testDB.Close()
	defer dbEnv.Close()

	mon := sync.Monitor{}
	conf := ManilaConf{Types: []string{"storage_pools"}}

	syncer := &ManilaSyncer{
		DB:         testDB,
		Mon:        mon,
		Conf:       conf,
		API:        &mockManilaAPI{},
		MqttClient: &mqtt.MockClient{},
	}

	ctx := t.Context()
	err := syncer.Sync(ctx)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
}

func TestManilaSyncer_SyncChangedStoragePools(t *testing.T) {
	dbEnv := testlibDB.SetupDBEnv(t)
	testDB := db.DB{DbMap: dbEnv.DbMap}
	defer testDB.Close()
	defer dbEnv.Close()

	mon := sync.Monitor{}
	conf := ManilaConf{Types: []string{"storage_pools"}}

	syncer := &ManilaSyncer{
		DB:   testDB,
		Mon:  mon,
		Conf: conf,
		API:  &mockManilaAPI{},
	}

	ctx := t.Context()
	pools, err := syncer.SyncChangedStoragePools(ctx)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if len(pools) != 1 {
		t.Fatalf("expected 1 storage pool, got %d", len(pools))
	}
}
