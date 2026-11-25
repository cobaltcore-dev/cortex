// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package cinder

import (
	"context"
	"testing"

	"github.com/cobaltcore-dev/cortex/api/v1alpha1"
	"github.com/cobaltcore-dev/cortex/pkg/knowledge/datasources"
	"github.com/cobaltcore-dev/cortex/pkg/knowledge/store/memory"
)

type mockCinderAPI struct{}

func (m *mockCinderAPI) Init(ctx context.Context) error { return nil }

func (m *mockCinderAPI) GetAllStoragePools(ctx context.Context) ([]StoragePool, error) {
	return []StoragePool{{Name: "pool1"}}, nil
}

func TestCinderSyncer_Init(t *testing.T) {
	mon := datasources.Monitor{}
	conf := v1alpha1.CinderDatasource{Type: v1alpha1.CinderDatasourceTypeStoragePools}

	syncer := &CinderSyncer{
		Store: memory.New(),
		Mon:   mon,
		Conf:  conf,
		API:   &mockCinderAPI{},
	}
	err := syncer.Init(t.Context())
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
}

func TestCinderSyncer_Sync(t *testing.T) {
	mon := datasources.Monitor{}
	conf := v1alpha1.CinderDatasource{Type: v1alpha1.CinderDatasourceTypeStoragePools}

	syncer := &CinderSyncer{
		Store: memory.New(),
		Mon:   mon,
		Conf:  conf,
		API:   &mockCinderAPI{},
	}

	ctx := t.Context()
	if err := syncer.Init(ctx); err != nil {
		t.Fatalf("failed to init cinder syncer: %v", err)
	}
	_, err := syncer.Sync(ctx)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
}

func TestCinderSyncer_SyncAllStoragePools(t *testing.T) {
	mon := datasources.Monitor{}
	conf := v1alpha1.CinderDatasource{Type: v1alpha1.CinderDatasourceTypeStoragePools}

	syncer := &CinderSyncer{
		Store: memory.New(),
		Mon:   mon,
		Conf:  conf,
		API:   &mockCinderAPI{},
	}

	ctx := t.Context()
	if err := syncer.Init(ctx); err != nil {
		t.Fatalf("failed to init cinder syncer: %v", err)
	}
	n, err := syncer.SyncAllStoragePools(ctx)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if n != 1 {
		t.Fatalf("expected 1 storage pool, got %d", n)
	}
}
