// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package limes

import (
	"context"
	"errors"
	"testing"

	"github.com/cobaltcore-dev/cortex/api/v1alpha1"
	"github.com/cobaltcore-dev/cortex/internal/knowledge/datasources"
	"github.com/cobaltcore-dev/cortex/internal/knowledge/datasources/openstack/identity"
	"github.com/cobaltcore-dev/cortex/pkg/db"
	testlibDB "github.com/cobaltcore-dev/cortex/pkg/db/testing"
	testlibKeystone "github.com/cobaltcore-dev/cortex/pkg/keystone/testing"
)

type mockLimesAPI struct{}

func (m *mockLimesAPI) Init(ctx context.Context) error { return nil }

func (m *mockLimesAPI) GetAllCommitments(ctx context.Context, projects []identity.Project) ([]Commitment, error) {
	if len(projects) == 0 {
		return []Commitment{}, nil
	}
	return []Commitment{
		{
			ID:               1,
			UUID:             "test-uuid-1",
			ServiceType:      "compute",
			ResourceName:     "cores",
			AvailabilityZone: "az1",
			Amount:           10,
			Unit:             "cores",
			Duration:         "1 year",
			CreatedAt:        1640995200,
			ExpiresAt:        1672531200,
			Transferable:     false,
			NotifyOnConfirm:  false,
			ProjectID:        projects[0].ID,
			DomainID:         projects[0].DomainID,
		},
	}, nil
}

func TestLimesSyncer_Init(t *testing.T) {
	dbEnv := testlibDB.SetupDBEnv(t)
	testDB := db.DB{DbMap: dbEnv.DbMap}
	defer dbEnv.Close()
	mon := datasources.Monitor{}
	k := &testlibKeystone.MockKeystoneAPI{}
	conf := v1alpha1.LimesDatasource{Type: v1alpha1.LimesDatasourceTypeProjectCommitments}

	syncer := &LimesSyncer{
		DB:   testDB,
		Mon:  mon,
		Conf: conf,
		API:  NewLimesAPI(mon, k, conf),
	}
	err := syncer.Init(t.Context())
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
}

func TestLimesSyncer_Sync(t *testing.T) {
	dbEnv := testlibDB.SetupDBEnv(t)
	testDB := db.DB{DbMap: dbEnv.DbMap}
	defer dbEnv.Close()
	// Insert a test project for the sync to use
	project := identity.Project{
		ID:       "test-project-id",
		DomainID: "test-domain-id",
		Name:     "test-project",
	}
	testDB.AddTable(identity.Project{})
	if err := testDB.CreateTable(testDB.AddTable(identity.Project{})); err != nil {
		t.Fatalf("failed to create project table: %v", err)
	}
	if err := testDB.Insert(&project); err != nil {
		t.Fatalf("failed to insert test project: %v", err)
	}

	mon := datasources.Monitor{}
	k := &testlibKeystone.MockKeystoneAPI{}
	conf := v1alpha1.LimesDatasource{Type: v1alpha1.LimesDatasourceTypeProjectCommitments}

	syncer := &LimesSyncer{
		DB:   testDB,
		Mon:  mon,
		Conf: conf,
		API:  NewLimesAPI(mon, k, conf),
	}
	syncer.API = &mockLimesAPI{}

	// Initialize the syncer to create the commitment table
	if err := syncer.Init(t.Context()); err != nil {
		t.Fatalf("failed to init limes syncer: %v", err)
	}

	ctx := t.Context()
	_, err := syncer.Sync(ctx)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
}

func TestLimesSyncer_SyncCommitments(t *testing.T) {
	dbEnv := testlibDB.SetupDBEnv(t)
	testDB := db.DB{DbMap: dbEnv.DbMap}
	defer dbEnv.Close()
	// Insert a test project for the sync to use
	project := identity.Project{
		ID:       "test-project-id",
		DomainID: "test-domain-id",
		Name:     "test-project",
	}
	testDB.AddTable(identity.Project{})
	if err := testDB.CreateTable(testDB.AddTable(identity.Project{})); err != nil {
		t.Fatalf("failed to create project table: %v", err)
	}
	if err := testDB.Insert(&project); err != nil {
		t.Fatalf("failed to insert test project: %v", err)
	}

	mon := datasources.Monitor{}
	k := &testlibKeystone.MockKeystoneAPI{}
	conf := v1alpha1.LimesDatasource{Type: v1alpha1.LimesDatasourceTypeProjectCommitments}

	syncer := &LimesSyncer{
		DB:   testDB,
		Mon:  mon,
		Conf: conf,
		API:  NewLimesAPI(mon, k, conf),
	}
	syncer.API = &mockLimesAPI{}

	ctx := t.Context()
	n, err := syncer.SyncCommitments(ctx)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if n != 1 {
		t.Fatalf("expected 1 commitment, got %d", n)
	}
}

func TestLimesSyncer_SyncCommitments_NoProjects(t *testing.T) {
	dbEnv := testlibDB.SetupDBEnv(t)
	testDB := db.DB{DbMap: dbEnv.DbMap}
	defer dbEnv.Close()
	// Create project table but don't insert any projects
	testDB.AddTable(identity.Project{})
	if err := testDB.CreateTable(testDB.AddTable(identity.Project{})); err != nil {
		t.Fatalf("failed to create project table: %v", err)
	}

	mon := datasources.Monitor{}
	k := &testlibKeystone.MockKeystoneAPI{}
	conf := v1alpha1.LimesDatasource{Type: v1alpha1.LimesDatasourceTypeProjectCommitments}

	syncer := &LimesSyncer{
		DB:   testDB,
		Mon:  mon,
		Conf: conf,
		API:  NewLimesAPI(mon, k, conf),
	}
	syncer.API = &mockLimesAPI{}

	ctx := t.Context()
	_, err := syncer.SyncCommitments(ctx)
	if !errors.Is(err, v1alpha1.ErrWaitingForDependencyDatasource) {
		t.Fatalf("expected ErrWaitingForDependencyDatasource, got %v", err)
	}
}
