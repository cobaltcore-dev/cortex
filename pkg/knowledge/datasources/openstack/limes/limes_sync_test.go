// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package limes

import (
	"context"
	"errors"
	"testing"

	"github.com/cobaltcore-dev/cortex/api/v1alpha1"
	testlibKeystone "github.com/cobaltcore-dev/cortex/pkg/keystone/testing"
	"github.com/cobaltcore-dev/cortex/pkg/knowledge/datasources"
	"github.com/cobaltcore-dev/cortex/pkg/knowledge/datasources/openstack/identity"
	"github.com/cobaltcore-dev/cortex/pkg/knowledge/store"
	"github.com/cobaltcore-dev/cortex/pkg/knowledge/store/memory"
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
	mon := datasources.Monitor{}
	k := &testlibKeystone.MockKeystoneClient{}
	conf := v1alpha1.LimesDatasource{Type: v1alpha1.LimesDatasourceTypeProjectCommitments}

	syncer := &LimesSyncer{
		Store: memory.New(),
		Mon:   mon,
		Conf:  conf,
		API:   NewLimesAPI(mon, k, conf),
	}
	err := syncer.Init(t.Context())
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
}

func TestLimesSyncer_Sync(t *testing.T) {
	// Insert a test project for the sync to use
	project := identity.Project{
		ID:       "test-project-id",
		DomainID: "test-domain-id",
		Name:     "test-project",
	}
	st := memory.New()
	if err := store.ReplaceAll(t.Context(), st, "", []identity.Project{project}); err != nil {
		t.Fatalf("failed to insert test project: %v", err)
	}

	mon := datasources.Monitor{}
	k := &testlibKeystone.MockKeystoneClient{}
	conf := v1alpha1.LimesDatasource{Type: v1alpha1.LimesDatasourceTypeProjectCommitments}

	syncer := &LimesSyncer{
		Store: st,
		Mon:   mon,
		Conf:  conf,
		API:   NewLimesAPI(mon, k, conf),
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
	// Insert a test project for the sync to use
	project := identity.Project{
		ID:       "test-project-id",
		DomainID: "test-domain-id",
		Name:     "test-project",
	}
	st := memory.New()
	if err := store.ReplaceAll(t.Context(), st, "", []identity.Project{project}); err != nil {
		t.Fatalf("failed to insert test project: %v", err)
	}

	mon := datasources.Monitor{}
	conf := v1alpha1.LimesDatasource{Type: v1alpha1.LimesDatasourceTypeProjectCommitments}

	syncer := &LimesSyncer{
		Store: st,
		Mon:   mon,
		Conf:  conf,
		API:   &mockLimesAPI{},
	}

	if err := syncer.Init(t.Context()); err != nil {
		t.Fatalf("failed to init limes syncer: %v", err)
	}

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
	// Store an empty project list.
	st := memory.New()
	if err := store.ReplaceAll(t.Context(), st, "", []identity.Project{}); err != nil {
		t.Fatalf("failed to store projects: %v", err)
	}

	mon := datasources.Monitor{}
	k := &testlibKeystone.MockKeystoneClient{}
	conf := v1alpha1.LimesDatasource{Type: v1alpha1.LimesDatasourceTypeProjectCommitments}

	syncer := &LimesSyncer{
		Store: st,
		Mon:   mon,
		Conf:  conf,
		API:   NewLimesAPI(mon, k, conf),
	}
	syncer.API = &mockLimesAPI{}

	ctx := t.Context()
	_, err := syncer.SyncCommitments(ctx)
	if !errors.Is(err, v1alpha1.ErrWaitingForDependencyDatasource) {
		t.Fatalf("expected ErrWaitingForDependencyDatasource, got %v", err)
	}
}
