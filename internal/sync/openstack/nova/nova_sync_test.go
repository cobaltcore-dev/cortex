// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package nova

import (
	"context"
	"testing"
	"time"

	"github.com/cobaltcore-dev/cortex/internal/db"
	"github.com/cobaltcore-dev/cortex/internal/sync"
	testlibDB "github.com/cobaltcore-dev/cortex/testlib/db"
	"github.com/cobaltcore-dev/cortex/testlib/mqtt"
)

type mockNovaAPI struct{}

func (m *mockNovaAPI) Init(ctx context.Context) {}

func (m *mockNovaAPI) GetChangedServers(ctx context.Context, t *time.Time) ([]Server, error) {
	return []Server{{ID: "1", Name: "server1"}}, nil
}

func (m *mockNovaAPI) GetChangedHypervisors(ctx context.Context, t *time.Time) ([]Hypervisor, error) {
	return []Hypervisor{{ID: 1, Hostname: "hypervisor1"}}, nil
}

func (m *mockNovaAPI) GetChangedFlavors(ctx context.Context, t *time.Time) ([]Flavor, error) {
	return []Flavor{{ID: "1", Name: "flavor1"}}, nil
}

func (m *mockNovaAPI) GetChangedMigrations(ctx context.Context, t *time.Time) ([]Migration, error) {
	return []Migration{{ID: 1}}, nil
}

func TestNovaSyncer_Init(t *testing.T) {
	dbEnv := testlibDB.SetupDBEnv(t)
	testDB := db.DB{DbMap: dbEnv.DbMap}
	defer testDB.Close()
	defer dbEnv.Close()

	mon := sync.Monitor{}
	syncer := &NovaSyncer{
		DB:   testDB,
		Mon:  mon,
		Conf: NovaConf{Types: []string{"servers", "hypervisors"}},
		API:  &mockNovaAPI{},
	}
	syncer.Init(t.Context())
}

func TestNovaSyncer_Sync(t *testing.T) {
	dbEnv := testlibDB.SetupDBEnv(t)
	testDB := db.DB{DbMap: dbEnv.DbMap}
	defer testDB.Close()
	defer dbEnv.Close()

	mon := sync.Monitor{}
	syncer := &NovaSyncer{
		DB:         testDB,
		Mon:        mon,
		Conf:       NovaConf{Types: []string{"servers", "hypervisors"}},
		API:        &mockNovaAPI{},
		MqttClient: &mqtt.MockClient{},
	}

	ctx := t.Context()
	syncer.Init(ctx)
	err := syncer.Sync(ctx)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
}

func TestNovaSyncer_SyncServers(t *testing.T) {
	dbEnv := testlibDB.SetupDBEnv(t)
	testDB := db.DB{DbMap: dbEnv.DbMap}
	defer testDB.Close()
	defer dbEnv.Close()

	mon := sync.Monitor{}
	syncer := &NovaSyncer{
		DB:   testDB,
		Mon:  mon,
		Conf: NovaConf{Types: []string{"servers", "hypervisors"}},
		API:  &mockNovaAPI{},
	}

	ctx := t.Context()
	syncer.Init(ctx)
	servers, err := syncer.SyncChangedServers(ctx)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if len(servers) != 1 {
		t.Fatalf("expected 1 server, got %d", len(servers))
	}
}

func TestNovaSyncer_SyncHypervisors(t *testing.T) {
	dbEnv := testlibDB.SetupDBEnv(t)
	testDB := db.DB{DbMap: dbEnv.DbMap}
	defer testDB.Close()
	defer dbEnv.Close()

	mon := sync.Monitor{}
	syncer := &NovaSyncer{
		DB:   testDB,
		Mon:  mon,
		Conf: NovaConf{Types: []string{"servers", "hypervisors"}},
		API:  &mockNovaAPI{},
	}

	ctx := t.Context()
	syncer.Init(ctx)
	hypervisors, err := syncer.SyncChangedHypervisors(ctx)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if len(hypervisors) != 1 {
		t.Fatalf("expected 1 hypervisor, got %d", len(hypervisors))
	}
}

func TestNovaSyncer_SyncFlavors(t *testing.T) {
	dbEnv := testlibDB.SetupDBEnv(t)
	testDB := db.DB{DbMap: dbEnv.DbMap}
	defer testDB.Close()
	defer dbEnv.Close()

	mon := sync.Monitor{}
	syncer := &NovaSyncer{
		DB:   testDB,
		Mon:  mon,
		Conf: NovaConf{Types: []string{"flavors"}},
		API:  &mockNovaAPI{},
	}

	ctx := t.Context()
	syncer.Init(ctx)
	flavors, err := syncer.SyncChangedFlavors(ctx)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if len(flavors) != 1 {
		t.Fatalf("expected 1 flavor, got %d", len(flavors))
	}
}

func TestNovaSyncer_SyncMigrations(t *testing.T) {
	dbEnv := testlibDB.SetupDBEnv(t)
	testDB := db.DB{DbMap: dbEnv.DbMap}
	defer testDB.Close()
	defer dbEnv.Close()

	mon := sync.Monitor{}
	syncer := &NovaSyncer{
		DB:   testDB,
		Mon:  mon,
		Conf: NovaConf{Types: []string{"migrations"}},
		API:  &mockNovaAPI{},
	}

	ctx := t.Context()
	syncer.Init(ctx)
	migrations, err := syncer.SyncChangedMigrations(ctx)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if len(migrations) != 1 {
		t.Fatalf("expected 1 migration, got %d", len(migrations))
	}
}
