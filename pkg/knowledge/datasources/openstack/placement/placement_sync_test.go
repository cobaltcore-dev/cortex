// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package placement

import (
	"context"
	"testing"

	"github.com/cobaltcore-dev/cortex/api/v1alpha1"
	testlibKeystone "github.com/cobaltcore-dev/cortex/pkg/keystone/testing"
	"github.com/cobaltcore-dev/cortex/pkg/knowledge/datasources"
	"github.com/cobaltcore-dev/cortex/pkg/knowledge/store"
	"github.com/cobaltcore-dev/cortex/pkg/knowledge/store/memory"
)

type mockPlacementAPI struct{}

func (m *mockPlacementAPI) Init(ctx context.Context) error { return nil }

func (m *mockPlacementAPI) GetAllResourceProviders(ctx context.Context) ([]ResourceProvider, error) {
	return []ResourceProvider{{UUID: "1", Name: "rp1"}}, nil
}

func (m *mockPlacementAPI) GetAllTraits(ctx context.Context, rps []ResourceProvider) ([]Trait, error) {
	return []Trait{{ResourceProviderUUID: "1", Name: "trait1"}}, nil
}

func (m *mockPlacementAPI) GetAllInventoryUsages(ctx context.Context, providers []ResourceProvider) ([]InventoryUsage, error) {
	return []InventoryUsage{{ResourceProviderUUID: "1", InventoryClassName: "vcpu"}}, nil
}

func TestPlacementSyncer_Init(t *testing.T) {
	mon := datasources.Monitor{}
	pc := &testlibKeystone.MockKeystoneClient{}
	conf := v1alpha1.PlacementDatasource{Type: v1alpha1.PlacementDatasourceTypeResourceProviders}

	syncer := &PlacementSyncer{
		Store: memory.New(),
		Mon:   mon,
		Conf:  conf,
		API:   NewPlacementAPI(mon, pc, conf),
	}
	err := syncer.Init(t.Context())
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
}

func TestPlacementSyncer_Sync(t *testing.T) {
	mon := datasources.Monitor{}
	conf := v1alpha1.PlacementDatasource{Type: v1alpha1.PlacementDatasourceTypeResourceProviders}

	syncer := &PlacementSyncer{
		Store: memory.New(),
		Mon:   mon,
		Conf:  conf,
		API:   &mockPlacementAPI{},
	}

	ctx := t.Context()
	if err := syncer.Init(ctx); err != nil {
		t.Fatalf("failed to init placement syncer: %v", err)
	}
	_, err := syncer.Sync(ctx)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
}

func TestPlacementSyncer_SyncResourceProviders(t *testing.T) {
	mon := datasources.Monitor{}
	conf := v1alpha1.PlacementDatasource{Type: v1alpha1.PlacementDatasourceTypeResourceProviders}

	syncer := &PlacementSyncer{
		Store: memory.New(),
		Mon:   mon,
		Conf:  conf,
		API:   &mockPlacementAPI{},
	}

	ctx := t.Context()
	if err := syncer.Init(ctx); err != nil {
		t.Fatalf("failed to init placement syncer: %v", err)
	}
	n, err := syncer.SyncResourceProviders(ctx)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if n != 1 {
		t.Fatalf("expected 1 resource provider, got %d", n)
	}
}

func TestPlacementSyncer_SyncTraits(t *testing.T) {
	mon := datasources.Monitor{}
	conf := v1alpha1.PlacementDatasource{Type: v1alpha1.PlacementDatasourceTypeResourceProviderTraits}

	rps := []ResourceProvider{{UUID: "1", Name: "rp1"}}
	st := memory.New()
	err := store.ReplaceAll(t.Context(), st, "", rps)
	if err != nil {
		t.Fatalf("failed to insert resource providers: %v", err)
	}

	syncer := &PlacementSyncer{
		Store: st,
		Mon:   mon,
		Conf:  conf,
		API:   &mockPlacementAPI{},
	}

	if err := syncer.Init(t.Context()); err != nil {
		t.Fatalf("failed to init placement syncer: %v", err)
	}

	ctx := t.Context()
	// First, we need to sync resource providers to have them in the DB.
	n, err := syncer.SyncTraits(ctx)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if n != 1 {
		t.Fatalf("expected 1 trait, got %d", n)
	}
}

func TestPlacementSyncer_SyncInventoryUsages(t *testing.T) {
	mon := datasources.Monitor{}
	conf := v1alpha1.PlacementDatasource{Type: v1alpha1.PlacementDatasourceTypeResourceProviderInventoryUsages}

	rps := []ResourceProvider{{UUID: "1", Name: "rp1"}}
	st := memory.New()
	err := store.ReplaceAll(t.Context(), st, "", rps)
	if err != nil {
		t.Fatalf("failed to insert resource providers: %v", err)
	}

	syncer := &PlacementSyncer{
		Store: st,
		Mon:   mon,
		Conf:  conf,
		API:   &mockPlacementAPI{},
	}

	if err := syncer.Init(t.Context()); err != nil {
		t.Fatalf("failed to init placement syncer: %v", err)
	}

	ctx := t.Context()
	n, err := syncer.SyncInventoryUsages(ctx)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if n != 1 {
		t.Fatalf("expected 1 inventory usage, got %d", n)
	}
}
