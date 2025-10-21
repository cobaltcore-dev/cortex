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
)

type mockCinderAPI struct{}

func (m *mockCinderAPI) Init(ctx context.Context) error { return nil }

func (m *mockCinderAPI) GetAllStoragePools(ctx context.Context) ([]cinder.StoragePool, error) {
	return []cinder.StoragePool{{Name: "pool1"}}, nil
}

func TestCinderSyncer_Init(t *testing.T) {
	dbEnv := testlibDB.SetupDBEnv(t)
	testDB := db.DB{DbMap: dbEnv.DbMap}
	defer testDB.Close()
	defer dbEnv.Close()

	mon := datasources.Monitor{}
	conf := v1alpha1.CinderDatasource{Type: v1alpha1.CinderDatasourceTypeStoragePools}

	syncer := &CinderSyncer{
		DB:   testDB,
		Mon:  mon,
		Conf: conf,
		API:  &mockCinderAPI{},
	}
	err := syncer.Init(t.Context())
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
}

func TestCinderSyncer_Sync(t *testing.T) {
	dbEnv := testlibDB.SetupDBEnv(t)
	testDB := db.DB{DbMap: dbEnv.DbMap}
	defer testDB.Close()
	defer dbEnv.Close()

	mon := datasources.Monitor{}
	conf := v1alpha1.CinderDatasource{Type: v1alpha1.CinderDatasourceTypeStoragePools}

	syncer := &CinderSyncer{
		DB:   testDB,
		Mon:  mon,
		Conf: conf,
		API:  &mockCinderAPI{},
	}

	ctx := t.Context()
	_, err := syncer.Sync(ctx)
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
	conf := v1alpha1.CinderDatasource{Type: v1alpha1.CinderDatasourceTypeStoragePools}

	syncer := &CinderSyncer{
		DB:   testDB,
		Mon:  mon,
		Conf: conf,
		API:  &mockCinderAPI{},
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
