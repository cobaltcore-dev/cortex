// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package nova

import (
	"context"
	"testing"
	"time"

	"github.com/cobaltcore-dev/cortex/api/v1alpha1"
	"github.com/cobaltcore-dev/cortex/internal/knowledge/datasources"
	"github.com/cobaltcore-dev/cortex/internal/knowledge/db"
	testlibDB "github.com/cobaltcore-dev/cortex/internal/knowledge/db/testing"
	testlib "github.com/cobaltcore-dev/cortex/pkg/testing"
)

type mockNovaAPI struct{}

func (m *mockNovaAPI) Init(ctx context.Context) error { return nil }

func (m *mockNovaAPI) GetAllServers(ctx context.Context) ([]Server, error) {
	return []Server{{ID: "1", Name: "server1"}}, nil
}

func (m *mockNovaAPI) GetDeletedServers(ctx context.Context, t time.Time) ([]DeletedServer, error) {
	// Mock different API responses based on the lookback time to test configuration behavior:
	// - If looking back more than 5 hours: return 2 servers (simulates more historical data)
	// - If looking back less than 5 hours: return 1 server (simulates less recent data)
	//
	// This allows testing:
	// 1. Default 6-hour lookback should get 2 servers (6h > 5h threshold)
	// 2. Custom 1-hour lookback should get 1 server (1h < 5h threshold)
	if t.Before(time.Now().Add(-5 * time.Hour)) {
		return []DeletedServer{
			{ID: "1", Name: "server1", Status: "DELETED"},
			{ID: "2", Name: "server2", Status: "DELETED"},
		}, nil
	}
	return []DeletedServer{{ID: "1", Name: "server1", Status: "DELETED"}}, nil
}

func (m *mockNovaAPI) GetAllHypervisors(ctx context.Context) ([]Hypervisor, error) {
	return []Hypervisor{{ID: "1", Hostname: "hypervisor1"}}, nil
}

func (m *mockNovaAPI) GetAllFlavors(ctx context.Context) ([]Flavor, error) {
	return []Flavor{{ID: "1", Name: "flavor1"}}, nil
}

func (m *mockNovaAPI) GetAllMigrations(ctx context.Context) ([]Migration, error) {
	return []Migration{{ID: 1}}, nil
}

func (m *mockNovaAPI) GetAllAggregates(ctx context.Context) ([]Aggregate, error) {
	return []Aggregate{{Name: "aggregate1"}}, nil
}

func TestNovaSyncer_Init(t *testing.T) {
	dbEnv := testlibDB.SetupDBEnv(t)
	testDB := db.DB{DbMap: dbEnv.DbMap}
	defer dbEnv.Close()
	mon := datasources.Monitor{}
	syncer := &NovaSyncer{
		DB:   testDB,
		Mon:  mon,
		Conf: v1alpha1.NovaDatasource{Type: v1alpha1.NovaDatasourceTypeServers},
		API:  &mockNovaAPI{},
	}
	err := syncer.Init(t.Context())
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
}

func TestNovaSyncer_Sync(t *testing.T) {
	dbEnv := testlibDB.SetupDBEnv(t)
	testDB := db.DB{DbMap: dbEnv.DbMap}
	defer dbEnv.Close()
	mon := datasources.Monitor{}
	syncer := &NovaSyncer{
		DB:   testDB,
		Mon:  mon,
		Conf: v1alpha1.NovaDatasource{Type: v1alpha1.NovaDatasourceTypeServers},
		API:  &mockNovaAPI{},
	}

	ctx := t.Context()
	if err := syncer.Init(ctx); err != nil {
		t.Fatalf("failed to init identity syncer: %v", err)
	}
	_, err := syncer.Sync(ctx)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
}

func TestNovaSyncer_SyncServers(t *testing.T) {
	dbEnv := testlibDB.SetupDBEnv(t)
	testDB := db.DB{DbMap: dbEnv.DbMap}
	defer dbEnv.Close()
	mon := datasources.Monitor{}
	syncer := &NovaSyncer{
		DB:   testDB,
		Mon:  mon,
		Conf: v1alpha1.NovaDatasource{Type: v1alpha1.NovaDatasourceTypeServers},
		API:  &mockNovaAPI{},
	}

	ctx := t.Context()
	if err := syncer.Init(ctx); err != nil {
		t.Fatalf("failed to init identity syncer: %v", err)
	}
	n, err := syncer.SyncAllServers(ctx)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if n != 1 {
		t.Fatalf("expected 1 server, got %d", n)
	}
}

func TestNovaSyncer_SyncDeletedServers(t *testing.T) {
	tests := []struct {
		Name                              string
		DeletedServersChangesSinceMinutes *int
		ExpectedAmountOfDeletedServers    int64
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
			defer dbEnv.Close()

			mon := datasources.Monitor{}
			syncer := &NovaSyncer{
				DB:  testDB,
				Mon: mon,
				Conf: v1alpha1.NovaDatasource{
					Type:                              v1alpha1.NovaDatasourceTypeDeletedServers,
					DeletedServersChangesSinceMinutes: tt.DeletedServersChangesSinceMinutes,
				},
				API: &mockNovaAPI{},
			}

			ctx := t.Context()
			if err := syncer.Init(ctx); err != nil {
				t.Fatalf("failed to init identity syncer: %v", err)
			}
			n, err := syncer.SyncDeletedServers(ctx)
			if err != nil {
				t.Fatalf("expected no error, got %v", err)
			}
			if n != tt.ExpectedAmountOfDeletedServers {
				t.Fatalf("expected %d server, got %d", tt.ExpectedAmountOfDeletedServers, n)
			}
		})
	}
}

func TestNovaSyncer_SyncHypervisors(t *testing.T) {
	dbEnv := testlibDB.SetupDBEnv(t)
	testDB := db.DB{DbMap: dbEnv.DbMap}
	defer dbEnv.Close()
	mon := datasources.Monitor{}
	syncer := &NovaSyncer{
		DB:   testDB,
		Mon:  mon,
		Conf: v1alpha1.NovaDatasource{Type: v1alpha1.NovaDatasourceTypeHypervisors},
		API:  &mockNovaAPI{},
	}

	ctx := t.Context()
	if err := syncer.Init(ctx); err != nil {
		t.Fatalf("failed to init identity syncer: %v", err)
	}
	n, err := syncer.SyncAllHypervisors(ctx)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if n != 1 {
		t.Fatalf("expected 1 hypervisor, got %d", n)
	}
}

func TestNovaSyncer_SyncFlavors(t *testing.T) {
	dbEnv := testlibDB.SetupDBEnv(t)
	testDB := db.DB{DbMap: dbEnv.DbMap}
	defer dbEnv.Close()
	mon := datasources.Monitor{}
	syncer := &NovaSyncer{
		DB:   testDB,
		Mon:  mon,
		Conf: v1alpha1.NovaDatasource{Type: v1alpha1.NovaDatasourceTypeFlavors},
		API:  &mockNovaAPI{},
	}

	ctx := t.Context()
	if err := syncer.Init(ctx); err != nil {
		t.Fatalf("failed to init identity syncer: %v", err)
	}
	n, err := syncer.SyncAllFlavors(ctx)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if n != 1 {
		t.Fatalf("expected 1 flavor, got %d", n)
	}
}

func TestNovaSyncer_SyncMigrations(t *testing.T) {
	dbEnv := testlibDB.SetupDBEnv(t)
	testDB := db.DB{DbMap: dbEnv.DbMap}
	defer dbEnv.Close()
	mon := datasources.Monitor{}
	syncer := &NovaSyncer{
		DB:   testDB,
		Mon:  mon,
		Conf: v1alpha1.NovaDatasource{Type: v1alpha1.NovaDatasourceTypeMigrations},
		API:  &mockNovaAPI{},
	}

	ctx := t.Context()
	if err := syncer.Init(ctx); err != nil {
		t.Fatalf("failed to init identity syncer: %v", err)
	}
	n, err := syncer.SyncAllMigrations(ctx)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if n != 1 {
		t.Fatalf("expected 1 migration, got %d", n)
	}
}

func TestNovaSyncer_SyncAggregates(t *testing.T) {
	dbEnv := testlibDB.SetupDBEnv(t)
	testDB := db.DB{DbMap: dbEnv.DbMap}
	defer dbEnv.Close()
	mon := datasources.Monitor{}
	syncer := &NovaSyncer{
		DB:   testDB,
		Mon:  mon,
		Conf: v1alpha1.NovaDatasource{Type: v1alpha1.NovaDatasourceTypeAggregates},
		API:  &mockNovaAPI{},
	}

	ctx := t.Context()
	if err := syncer.Init(ctx); err != nil {
		t.Fatalf("failed to init identity syncer: %v", err)
	}
	n, err := syncer.SyncAllAggregates(ctx)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if n != 1 {
		t.Fatalf("expected 1 aggregate, got %d", n)
	}
}
