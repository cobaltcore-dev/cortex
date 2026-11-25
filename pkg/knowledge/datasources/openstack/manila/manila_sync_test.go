// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package manila

import (
	"context"
	"testing"

	"github.com/cobaltcore-dev/cortex/api/v1alpha1"
	"github.com/cobaltcore-dev/cortex/pkg/knowledge/datasources"
	"github.com/cobaltcore-dev/cortex/pkg/knowledge/store/memory"
)

type mockManilaAPI struct{}

func (m *mockManilaAPI) Init(ctx context.Context) error { return nil }

func (m *mockManilaAPI) GetAllStoragePools(ctx context.Context) ([]StoragePool, error) {
	return []StoragePool{{Name: "pool1", Host: "host1", Backend: "backend1", Pool: "poolA"}}, nil
}

func TestManilaSyncer_Init(t *testing.T) {
	mon := datasources.Monitor{}
	conf := v1alpha1.ManilaDatasource{Type: v1alpha1.ManilaDatasourceTypeStoragePools}

	syncer := &ManilaSyncer{
		Store: memory.New(),
		Mon:   mon,
		Conf:  conf,
		API:   &mockManilaAPI{},
	}
	err := syncer.Init(t.Context())
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
}

func TestManilaSyncer_Sync(t *testing.T) {
	mon := datasources.Monitor{}
	conf := v1alpha1.ManilaDatasource{Type: v1alpha1.ManilaDatasourceTypeStoragePools}

	syncer := &ManilaSyncer{
		Store: memory.New(),
		Mon:   mon,
		Conf:  conf,
		API:   &mockManilaAPI{},
	}

	ctx := t.Context()
	if err := syncer.Init(ctx); err != nil {
		t.Fatalf("failed to init manila syncer: %v", err)
	}
	_, err := syncer.Sync(ctx)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
}

func TestManilaSyncer_SyncAllStoragePools(t *testing.T) {
	mon := datasources.Monitor{}
	conf := v1alpha1.ManilaDatasource{Type: v1alpha1.ManilaDatasourceTypeStoragePools}

	syncer := &ManilaSyncer{
		Store: memory.New(),
		Mon:   mon,
		Conf:  conf,
		API:   &mockManilaAPI{},
	}

	ctx := t.Context()
	if err := syncer.Init(ctx); err != nil {
		t.Fatalf("failed to init manila syncer: %v", err)
	}
	n, err := syncer.SyncAllStoragePools(ctx)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if n != 1 {
		t.Fatalf("expected 1 storage pool, got %d", n)
	}
}
