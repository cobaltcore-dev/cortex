// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package identity

import (
	"context"
	"testing"

	"github.com/cobaltcore-dev/cortex/api/v1alpha1"
	"github.com/cobaltcore-dev/cortex/internal/knowledge/datasources"
	"github.com/cobaltcore-dev/cortex/pkg/db"
	testlibDB "github.com/cobaltcore-dev/cortex/pkg/db/testing"
)

type mockIdentityAPI struct{}

func (m *mockIdentityAPI) Init(ctx context.Context) error { return nil }

func (m *mockIdentityAPI) GetAllDomains(ctx context.Context) ([]Domain, error) {
	return []Domain{{ID: "1", Name: "domain1", Enabled: true}}, nil
}

func (m *mockIdentityAPI) GetAllProjects(ctx context.Context) ([]Project, error) {
	return []Project{{ID: "1", Name: "project1", DomainID: "domain1"}}, nil
}

func TestIdentitySyncer_Init(t *testing.T) {
	dbEnv := testlibDB.SetupDBEnv(t)
	testDB := db.DB{DbMap: dbEnv.DbMap}
	defer dbEnv.Close()
	mon := datasources.Monitor{}
	syncer := &IdentitySyncer{
		DB:   testDB,
		Mon:  mon,
		Conf: v1alpha1.IdentityDatasource{Type: v1alpha1.IdentityDatasourceTypeDomains},
		API:  &mockIdentityAPI{},
	}
	err := syncer.Init(t.Context())
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
}

func TestIdentitySyncer_Sync(t *testing.T) {
	dbEnv := testlibDB.SetupDBEnv(t)
	testDB := db.DB{DbMap: dbEnv.DbMap}
	defer dbEnv.Close()
	mon := datasources.Monitor{}
	syncer := &IdentitySyncer{
		DB:   testDB,
		Mon:  mon,
		Conf: v1alpha1.IdentityDatasource{Type: v1alpha1.IdentityDatasourceTypeProjects},
		API:  &mockIdentityAPI{},
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

func TestIdentitySyncer_SyncProjects(t *testing.T) {
	dbEnv := testlibDB.SetupDBEnv(t)
	testDB := db.DB{DbMap: dbEnv.DbMap}
	defer dbEnv.Close()
	mon := datasources.Monitor{}
	syncer := &IdentitySyncer{
		DB:   testDB,
		Mon:  mon,
		Conf: v1alpha1.IdentityDatasource{Type: v1alpha1.IdentityDatasourceTypeProjects},
		API:  &mockIdentityAPI{},
	}

	ctx := t.Context()
	if err := syncer.Init(ctx); err != nil {
		t.Fatalf("failed to init identity syncer: %v", err)
	}
	n, err := syncer.SyncProjects(ctx)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if n != 1 {
		t.Fatalf("expected 1 server, got %d", n)
	}
}

func TestIdentitySyncer_SyncDomains(t *testing.T) {
	dbEnv := testlibDB.SetupDBEnv(t)
	testDB := db.DB{DbMap: dbEnv.DbMap}
	defer dbEnv.Close()
	mon := datasources.Monitor{}
	syncer := &IdentitySyncer{
		DB:   testDB,
		Mon:  mon,
		Conf: v1alpha1.IdentityDatasource{Type: v1alpha1.IdentityDatasourceTypeDomains},
		API:  &mockIdentityAPI{},
	}

	ctx := t.Context()
	if err := syncer.Init(ctx); err != nil {
		t.Fatalf("failed to init identity syncer: %v", err)
	}
	n, err := syncer.SyncDomains(ctx)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if n != 1 {
		t.Fatalf("expected 1 server, got %d", n)
	}
}
