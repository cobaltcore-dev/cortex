// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package manila

import (
	"context"
	"testing"

	"github.com/cobaltcore-dev/cortex/knowledge/api/datasources/openstack/manila"
	"github.com/cobaltcore-dev/cortex/knowledge/api/v1alpha1"
	"github.com/cobaltcore-dev/cortex/knowledge/internal/datasources"
	"github.com/cobaltcore-dev/cortex/lib/db"
	testlibDB "github.com/cobaltcore-dev/cortex/testlib/db"
)

type mockManilaAPI struct{}

func (m *mockManilaAPI) Init(ctx context.Context) {}

func (m *mockManilaAPI) GetAllStoragePools(ctx context.Context) ([]manila.StoragePool, error) {
	return []manila.StoragePool{{Name: "pool1", Host: "host1", Backend: "backend1", Pool: "poolA"}}, nil
}

func TestManilaSyncer_Init(t *testing.T) {
	dbEnv := testlibDB.SetupDBEnv(t)
	testDB := db.DB{DbMap: dbEnv.DbMap}
	defer testDB.Close()
	defer dbEnv.Close()

	mon := datasources.Monitor{}
	conf := v1alpha1.ManilaDatasource{Type: v1alpha1.ManilaDatasourceTypeStoragePools}

	syncer := &ManilaSyncer{
		DB:   testDB,
		Mon:  mon,
		Conf: conf,
		API:  &mockManilaAPI{},
	}
	err := syncer.Init(t.Context())
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
}

func TestManilaSyncer_Sync(t *testing.T) {
	dbEnv := testlibDB.SetupDBEnv(t)
	testDB := db.DB{DbMap: dbEnv.DbMap}
	defer testDB.Close()
	defer dbEnv.Close()

	mon := datasources.Monitor{}
	conf := v1alpha1.ManilaDatasource{Type: v1alpha1.ManilaDatasourceTypeStoragePools}

	syncer := &ManilaSyncer{
		DB:   testDB,
		Mon:  mon,
		Conf: conf,
		API:  &mockManilaAPI{},
	}

	ctx := t.Context()
	_, err := syncer.Sync(ctx)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
}

func TestManilaSyncer_SyncAllStoragePools(t *testing.T) {
	dbEnv := testlibDB.SetupDBEnv(t)
	testDB := db.DB{DbMap: dbEnv.DbMap}
	defer testDB.Close()
	defer dbEnv.Close()

	mon := datasources.Monitor{}
	conf := v1alpha1.ManilaDatasource{Type: v1alpha1.ManilaDatasourceTypeStoragePools}

	syncer := &ManilaSyncer{
		DB:   testDB,
		Mon:  mon,
		Conf: conf,
		API:  &mockManilaAPI{},
	}

	ctx := t.Context()
	n, err := syncer.SyncAllStoragePools(ctx)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if n != 1 {
		t.Fatalf("expected 1 storage pool, got %d", n)
	}
}
