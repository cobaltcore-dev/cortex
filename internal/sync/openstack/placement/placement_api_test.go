// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package placement

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/cobaltcore-dev/cortex/internal/keystone"
	"github.com/cobaltcore-dev/cortex/internal/sync"
	testlibKeystone "github.com/cobaltcore-dev/cortex/testlib/keystone"
)

func setupPlacementMockServer(handler http.HandlerFunc) (*httptest.Server, keystone.KeystoneAPI) {
	server := httptest.NewServer(handler)
	return server, &testlibKeystone.MockKeystoneAPI{Url: server.URL + "/"}
}

func TestNewPlacementAPI(t *testing.T) {
	mon := sync.Monitor{}
	k := &testlibKeystone.MockKeystoneAPI{}
	conf := PlacementConf{}

	api := NewPlacementAPI(mon, k, conf)
	if api == nil {
		t.Fatal("expected non-nil api")
	}
}

func TestPlacementAPI_GetAllResourceProviders(t *testing.T) {
	handler := func(w http.ResponseWriter, r *http.Request) {
		w.Header().Add("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		if _, err := w.Write([]byte(`{"resource_providers": [{"uuid": "1", "name": "rp1", "parent_provider_uuid": "pp1", "root_provider_uuid": "rootp1", "resource_provider_generation": 1}]}`)); err != nil {
			t.Fatalf("failed to write response: %v", err)
		}
	}
	server, k := setupPlacementMockServer(handler)
	defer server.Close()

	mon := sync.Monitor{}
	conf := PlacementConf{}

	api := NewPlacementAPI(mon, k, conf).(*placementAPI)
	api.Init(t.Context())

	ctx := t.Context()
	rps, err := api.GetAllResourceProviders(ctx)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if len(rps) != 1 {
		t.Fatalf("expected 1 resource provider, got %d", len(rps))
	}
}

func TestPlacementAPI_GetAllTraits(t *testing.T) {
	handler := func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/resource_providers/1/traits" {
			w.WriteHeader(http.StatusOK)
			if _, err := w.Write([]byte(`{"traits": ["trait1"]}`)); err != nil {
				t.Fatalf("failed to write response: %v", err)
			}
		} else {
			w.WriteHeader(http.StatusNotFound)
		}
	}
	server, pc := setupPlacementMockServer(handler)
	defer server.Close()

	mon := sync.Monitor{}
	conf := PlacementConf{}

	api := NewPlacementAPI(mon, pc, conf).(*placementAPI)
	api.Init(t.Context())

	ctx := t.Context()
	providers := []ResourceProvider{{UUID: "1", Name: "rp1"}}
	traits, err := api.GetAllTraits(ctx, providers)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if len(traits) != 1 {
		t.Fatalf("expected 1 trait, got %d", len(traits))
	}
}

func TestPlacementAPI_GetAllTraits_Error(t *testing.T) {
	handler := func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/resource_providers/error/traits" {
			w.WriteHeader(http.StatusInternalServerError)
			if _, err := w.Write([]byte(`{"error": "error fetching traits"}`)); err != nil {
				t.Fatalf("failed to write response: %v", err)
			}
		} else {
			w.WriteHeader(http.StatusNotFound)
		}
	}
	server, pc := setupPlacementMockServer(handler)
	defer server.Close()

	mon := sync.Monitor{}
	conf := PlacementConf{}

	api := NewPlacementAPI(mon, pc, conf).(*placementAPI)
	api.Init(t.Context())

	ctx := t.Context()
	providers := []ResourceProvider{{UUID: "error", Name: "rp1"}}
	_, err := api.GetAllTraits(ctx, providers)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
}

func TestPlacementAPI_GetAllInventoryUsages(t *testing.T) {
	handler := http.NewServeMux()

	handler.HandleFunc("/resource_providers", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Add("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, err := w.Write([]byte(`{"resource_providers": [{"uuid": "1", "name": "rp1", "parent_provider_uuid": "pp1", "root_provider_uuid": "rootp1", "resource_provider_generation": 1}]}`))
		if err != nil {
			t.Fatalf("failed to write response: %v", err)
		}
	})

	handler.HandleFunc("/resource_providers/1/inventories", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Add("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, err := w.Write([]byte(`{"inventories": {"VCPU": {"allocation_ratio": 16, "max_unit": 8, "min_unit": 1, "reserved": 0, "step_size": 1, "total": 8}}}`))
		if err != nil {
			t.Fatalf("failed to write response: %v", err)
		}
	})

	handler.HandleFunc("/resource_providers/1/usages", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Add("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, err := w.Write([]byte(`{"usages": {"VCPU": 4}}`))
		if err != nil {
			t.Fatalf("failed to write response: %v", err)
		}
	})

	server := httptest.NewServer(handler)
	defer server.Close()

	mon := sync.Monitor{}
	conf := PlacementConf{}
	k := &testlibKeystone.MockKeystoneAPI{Url: server.URL + "/"}

	api := NewPlacementAPI(mon, k, conf).(*placementAPI)
	api.Init(t.Context())

	ctx := t.Context()
	providers := []ResourceProvider{{UUID: "1", Name: "rp1", ResourceProviderGeneration: 1}}
	invUsages, err := api.GetAllInventoryUsages(ctx, providers)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if len(invUsages) != 1 {
		t.Fatalf("expected 1 inventory usage, got %d", len(invUsages))
	}
	inv := invUsages[0]
	if inv.InventoryClassName != "VCPU" || inv.Used != 4 || inv.Total != 8 {
		t.Errorf("unexpected inventory usage: %+v", inv)
	}
}
