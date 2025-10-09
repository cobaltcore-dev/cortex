// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package nova

import (
	"context"
	"testing"
	"time"

	"github.com/cobaltcore-dev/cortex/lib/db"
	"github.com/cobaltcore-dev/cortex/sync/api/objects/openstack/nova"
	sync "github.com/cobaltcore-dev/cortex/sync/internal"
	"github.com/cobaltcore-dev/cortex/sync/internal/conf"
	"github.com/cobaltcore-dev/cortex/testlib"
	testlibDB "github.com/cobaltcore-dev/cortex/testlib/db"
	"github.com/cobaltcore-dev/cortex/testlib/mqtt"
)

type mockNovaAPI struct{}

func (m *mockNovaAPI) Init(ctx context.Context) {}

func (m *mockNovaAPI) GetAllServers(ctx context.Context) ([]nova.Server, error) {
	return []nova.Server{{ID: "1", Name: "server1"}}, nil
}

func (m *mockNovaAPI) GetDeletedServers(ctx context.Context, t time.Time) ([]nova.DeletedServer, error) {
	// Mock different API responses based on the lookback time to test configuration behavior:
	// - If looking back more than 5 hours: return 2 servers (simulates more historical data)
	// - If looking back less than 5 hours: return 1 server (simulates less recent data)
	//
	// This allows testing:
	// 1. Default 6-hour lookback should get 2 servers (6h > 5h threshold)
	// 2. Custom 1-hour lookback should get 1 server (1h < 5h threshold)
	if t.Before(time.Now().Add(-5 * time.Hour)) {
		return []nova.DeletedServer{
			{ID: "1", Name: "server1", Status: "DELETED"},
			{ID: "2", Name: "server2", Status: "DELETED"},
		}, nil
	}
	return []nova.DeletedServer{{ID: "1", Name: "server1", Status: "DELETED"}}, nil
}

func (m *mockNovaAPI) GetAllHypervisors(ctx context.Context) ([]nova.Hypervisor, error) {
	return []nova.Hypervisor{{ID: "1", Hostname: "hypervisor1"}}, nil
}

func (m *mockNovaAPI) GetAllFlavors(ctx context.Context) ([]nova.Flavor, error) {
	return []nova.Flavor{{ID: "1", Name: "flavor1"}}, nil
}

func (m *mockNovaAPI) GetAllMigrations(ctx context.Context) ([]nova.Migration, error) {
	return []nova.Migration{{ID: 1}}, nil
}

func (m *mockNovaAPI) GetAllAggregates(ctx context.Context) ([]nova.Aggregate, error) {
	return []nova.Aggregate{{Name: "aggregate1"}}, nil
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
		Conf: conf.SyncOpenStackNovaConfig{Types: []string{"servers", "hypervisors"}},
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
		Conf:       conf.SyncOpenStackNovaConfig{Types: []string{"servers", "hypervisors"}},
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
		Conf: conf.SyncOpenStackNovaConfig{Types: []string{"servers"}},
		API:  &mockNovaAPI{},
	}

	ctx := t.Context()
	syncer.Init(ctx)
	servers, err := syncer.SyncAllServers(ctx)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if len(servers) != 1 {
		t.Fatalf("expected 1 server, got %d", len(servers))
	}
}

func TestNovaSyncer_SyncDeletedServers(t *testing.T) {
	tests := []struct {
		Name                              string
		DeletedServersChangesSinceMinutes *int
		ExpectedAmountOfDeletedServers    int
	}{
		{
			Name:                              "default time",
			DeletedServersChangesSinceMinutes: nil, // should default to 6 hours
			ExpectedAmountOfDeletedServers:    2,
		},
		{
			Name:                              "custom time",
			DeletedServersChangesSinceMinutes: testlib.Ptr(60),
			ExpectedAmountOfDeletedServers:    1,
		},
	}
	for _, tt := range tests {
		t.Run(tt.Name, func(t *testing.T) {
			dbEnv := testlibDB.SetupDBEnv(t)
			testDB := db.DB{DbMap: dbEnv.DbMap}
			defer testDB.Close()
			defer dbEnv.Close()

			mon := sync.Monitor{}
			syncer := &NovaSyncer{
				DB:  testDB,
				Mon: mon,
				Conf: conf.SyncOpenStackNovaConfig{
					Types:                             []string{"deleted_servers"},
					DeletedServersChangesSinceMinutes: tt.DeletedServersChangesSinceMinutes,
				},
				API: &mockNovaAPI{},
			}

			ctx := t.Context()
			syncer.Init(ctx)
			servers, err := syncer.SyncDeletedServers(ctx)
			if err != nil {
				t.Fatalf("expected no error, got %v", err)
			}
			if len(servers) != tt.ExpectedAmountOfDeletedServers {
				t.Fatalf("expected %d server, got %d", tt.ExpectedAmountOfDeletedServers, len(servers))
			}
		})
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
		Conf: conf.SyncOpenStackNovaConfig{Types: []string{"hypervisors"}},
		API:  &mockNovaAPI{},
	}

	ctx := t.Context()
	syncer.Init(ctx)
	hypervisors, err := syncer.SyncAllHypervisors(ctx)
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
		Conf: conf.SyncOpenStackNovaConfig{Types: []string{"flavors"}},
		API:  &mockNovaAPI{},
	}

	ctx := t.Context()
	syncer.Init(ctx)
	flavors, err := syncer.SyncAllFlavors(ctx)
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
		Conf: conf.SyncOpenStackNovaConfig{Types: []string{"migrations"}},
		API:  &mockNovaAPI{},
	}

	ctx := t.Context()
	syncer.Init(ctx)
	migrations, err := syncer.SyncAllMigrations(ctx)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if len(migrations) != 1 {
		t.Fatalf("expected 1 migration, got %d", len(migrations))
	}
}

func TestNovaSyncer_SyncAggregates(t *testing.T) {
	dbEnv := testlibDB.SetupDBEnv(t)
	testDB := db.DB{DbMap: dbEnv.DbMap}
	defer testDB.Close()
	defer dbEnv.Close()

	mon := sync.Monitor{}
	syncer := &NovaSyncer{
		DB:   testDB,
		Mon:  mon,
		Conf: conf.SyncOpenStackNovaConfig{Types: []string{"aggregates"}},
		API:  &mockNovaAPI{},
	}

	ctx := t.Context()
	syncer.Init(ctx)
	aggregates, err := syncer.SyncAllAggregates(ctx)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if len(aggregates) != 1 {
		t.Fatalf("expected 1 aggregate, got %d", len(aggregates))
	}
}
