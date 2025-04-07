// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package openstack

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

func (m *mockNovaAPI) GetAllServers(ctx context.Context, t *time.Time) ([]Server, error) {
	return []Server{{ID: "1", Name: "server1"}}, nil
}

func (m *mockNovaAPI) GetAllHypervisors(ctx context.Context, t *time.Time) ([]Hypervisor, error) {
	return []Hypervisor{{ID: 1, Hostname: "hypervisor1"}}, nil
}

func (m *mockNovaAPI) GetAllFlavors(ctx context.Context, t *time.Time) ([]Flavor, error) {
	return []Flavor{{ID: "1", Name: "flavor1"}}, nil
}

func TestNovaSyncer_Init(t *testing.T) {
	dbEnv := testlibDB.SetupDBEnv(t)
	testDB := db.DB{DbMap: dbEnv.DbMap}
	defer testDB.Close()
	defer dbEnv.Close()

	mon := sync.Monitor{}
	syncer := &novaSyncer{
		db:   testDB,
		mon:  mon,
		conf: NovaConf{Types: []string{"servers", "hypervisors"}},
		api:  &mockNovaAPI{},
	}
	syncer.Init(t.Context())
}

func TestNovaSyncer_Sync(t *testing.T) {
	dbEnv := testlibDB.SetupDBEnv(t)
	testDB := db.DB{DbMap: dbEnv.DbMap}
	defer testDB.Close()
	defer dbEnv.Close()

	mon := sync.Monitor{}
	syncer := &novaSyncer{
		db:         testDB,
		mon:        mon,
		conf:       NovaConf{Types: []string{"servers", "hypervisors"}},
		api:        &mockNovaAPI{},
		mqttClient: &mqtt.MockClient{},
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
	syncer := &novaSyncer{
		db:   testDB,
		mon:  mon,
		conf: NovaConf{Types: []string{"servers", "hypervisors"}},
		api:  &mockNovaAPI{},
	}

	ctx := t.Context()
	syncer.Init(ctx)
	servers, err := syncer.SyncServers(ctx)
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
	syncer := &novaSyncer{
		db:   testDB,
		mon:  mon,
		conf: NovaConf{Types: []string{"servers", "hypervisors"}},
		api:  &mockNovaAPI{},
	}

	ctx := t.Context()
	syncer.Init(ctx)
	hypervisors, err := syncer.SyncHypervisors(ctx)
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
	syncer := &novaSyncer{
		db:   testDB,
		mon:  mon,
		conf: NovaConf{Types: []string{"flavors"}},
		api:  &mockNovaAPI{},
	}

	ctx := t.Context()
	syncer.Init(ctx)
	flavors, err := syncer.SyncFlavors(ctx)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if len(flavors) != 1 {
		t.Fatalf("expected 1 flavor, got %d", len(flavors))
	}
}
