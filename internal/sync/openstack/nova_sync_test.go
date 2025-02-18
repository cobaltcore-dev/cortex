// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package openstack

import (
	"context"
	"testing"

	"github.com/cobaltcore-dev/cortex/internal/db"
	"github.com/cobaltcore-dev/cortex/internal/sync"
	testlibDB "github.com/cobaltcore-dev/cortex/testlib/db"
)

type mockNovaAPI struct{}

func (m *mockNovaAPI) Init(ctx context.Context) {}

func (m *mockNovaAPI) GetAllServers(ctx context.Context) ([]Server, error) {
	return []Server{{ID: "1", Name: "server1"}}, nil
}

func (m *mockNovaAPI) GetAllHypervisors(ctx context.Context) ([]Hypervisor, error) {
	return []Hypervisor{{ID: 1, Hostname: "hypervisor1"}}, nil
}

func TestNewNovaSyncer(t *testing.T) {
	dbEnv := testlibDB.SetupDBEnv(t)
	testDB := db.DB{DbMap: dbEnv.DbMap}
	defer testDB.Close()
	defer dbEnv.Close()

	mon := sync.Monitor{}
	pc := &mockKeystoneAPI{}
	conf := NovaConf{Types: []string{"servers", "hypervisors"}}

	syncer := newNovaSyncer(testDB, mon, pc, conf)
	if syncer == nil {
		t.Fatal("expected non-nil syncer")
	}
}

func TestNovaSyncer_Init(t *testing.T) {
	dbEnv := testlibDB.SetupDBEnv(t)
	testDB := db.DB{DbMap: dbEnv.DbMap}
	defer testDB.Close()
	defer dbEnv.Close()

	mon := sync.Monitor{}
	pc := &mockKeystoneAPI{}
	conf := NovaConf{Types: []string{"servers", "hypervisors"}}

	syncer := newNovaSyncer(testDB, mon, pc, conf).(*novaSyncer)
	syncer.Init(context.Background())
}

func TestNovaSyncer_Sync(t *testing.T) {
	dbEnv := testlibDB.SetupDBEnv(t)
	testDB := db.DB{DbMap: dbEnv.DbMap}
	defer testDB.Close()
	defer dbEnv.Close()

	mon := sync.Monitor{}
	pc := &mockKeystoneAPI{}
	conf := NovaConf{Types: []string{"servers", "hypervisors"}}

	syncer := newNovaSyncer(testDB, mon, pc, conf).(*novaSyncer)
	syncer.api = &mockNovaAPI{}

	ctx := context.Background()
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
	pc := &mockKeystoneAPI{}
	conf := NovaConf{Types: []string{"servers", "hypervisors"}}

	syncer := newNovaSyncer(testDB, mon, pc, conf).(*novaSyncer)
	syncer.api = &mockNovaAPI{}

	ctx := context.Background()
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
	pc := &mockKeystoneAPI{}
	conf := NovaConf{Types: []string{"servers", "hypervisors"}}

	syncer := newNovaSyncer(testDB, mon, pc, conf).(*novaSyncer)
	syncer.api = &mockNovaAPI{}

	ctx := context.Background()
	hypervisors, err := syncer.SyncHypervisors(ctx)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if len(hypervisors) != 1 {
		t.Fatalf("expected 1 hypervisor, got %d", len(hypervisors))
	}
}
