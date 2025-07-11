// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package identity

import (
	"context"
	"testing"

	"github.com/cobaltcore-dev/cortex/internal/db"
	"github.com/cobaltcore-dev/cortex/internal/sync"
	testlibDB "github.com/cobaltcore-dev/cortex/testlib/db"
	"github.com/cobaltcore-dev/cortex/testlib/mqtt"
)

type mockIdentityAPI struct{}

func (m *mockIdentityAPI) Init(ctx context.Context) {}

func (m *mockIdentityAPI) GetAllDomains(ctx context.Context) ([]Domain, error) {
	return []Domain{{ID: "1", Name: "domain1", Enabled: true}}, nil
}

func (m *mockIdentityAPI) GetAllProjects(ctx context.Context) ([]Project, error) {
	return []Project{{ID: "1", Name: "project1", DomainID: "domain1"}}, nil
}

func TestIdentitySyncer_Init(t *testing.T) {
	dbEnv := testlibDB.SetupDBEnv(t)
	testDB := db.DB{DbMap: dbEnv.DbMap}
	defer testDB.Close()
	defer dbEnv.Close()

	mon := sync.Monitor{}
	syncer := &IdentitySyncer{
		DB:   testDB,
		Mon:  mon,
		Conf: IdentityConf{Types: []string{"projects", "domains"}},
		API:  &mockIdentityAPI{},
	}
	syncer.Init(t.Context())
}

func TestIdentitySyncer_Sync(t *testing.T) {
	dbEnv := testlibDB.SetupDBEnv(t)
	testDB := db.DB{DbMap: dbEnv.DbMap}
	defer testDB.Close()
	defer dbEnv.Close()

	mon := sync.Monitor{}
	syncer := &IdentitySyncer{
		DB:         testDB,
		Mon:        mon,
		Conf:       IdentityConf{Types: []string{"projects", "domains"}},
		API:        &mockIdentityAPI{},
		MqttClient: &mqtt.MockClient{},
	}

	ctx := t.Context()
	syncer.Init(ctx)
	err := syncer.Sync(ctx)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
}

func TestIdentitySyncer_SyncProjects(t *testing.T) {
	dbEnv := testlibDB.SetupDBEnv(t)
	testDB := db.DB{DbMap: dbEnv.DbMap}
	defer testDB.Close()
	defer dbEnv.Close()

	mon := sync.Monitor{}
	syncer := &IdentitySyncer{
		DB:   testDB,
		Mon:  mon,
		Conf: IdentityConf{Types: []string{"projects", "domains"}},
		API:  &mockIdentityAPI{},
	}

	ctx := t.Context()
	syncer.Init(ctx)
	projects, err := syncer.SyncProjects(ctx)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	amountProjects := len(projects)
	if amountProjects != 1 {
		t.Fatalf("expected 1 server, got %d", amountProjects)
	}
}

func TestIdentitySyncer_SyncDomains(t *testing.T) {
	dbEnv := testlibDB.SetupDBEnv(t)
	testDB := db.DB{DbMap: dbEnv.DbMap}
	defer testDB.Close()
	defer dbEnv.Close()

	mon := sync.Monitor{}
	syncer := &IdentitySyncer{
		DB:   testDB,
		Mon:  mon,
		Conf: IdentityConf{Types: []string{"projects", "domains"}},
		API:  &mockIdentityAPI{},
	}

	ctx := t.Context()
	syncer.Init(ctx)
	domains, err := syncer.SyncDomains(ctx)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	amountDomains := len(domains)
	if amountDomains != 1 {
		t.Fatalf("expected 1 server, got %d", amountDomains)
	}
}
