// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package openstack

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/cobaltcore-dev/cortex/internal/sync"
)

func setupNovaMockServer(handler http.HandlerFunc) (*httptest.Server, KeystoneAPI) {
	server := httptest.NewServer(handler)
	return server, &mockKeystoneAPI{url: server.URL + "/"}
}

func TestNewNovaAPI(t *testing.T) {
	mon := sync.Monitor{}
	k := &mockKeystoneAPI{}
	conf := NovaConf{}

	api := newNovaAPI(mon, k, conf)
	if api == nil {
		t.Fatal("expected non-nil api")
	}
}

func TestNovaAPI_GetAllServers(t *testing.T) {
	handler := func(w http.ResponseWriter, r *http.Request) {
		w.Header().Add("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		if _, err := w.Write([]byte(`{"servers": [{
            "id": "1",
            "name": "server1",
			"flavor": {"id": "1", "name": "flavor1"}
        }]}`)); err != nil {
			t.Fatalf("failed to write response: %v", err)
		}
	}
	server, k := setupNovaMockServer(handler)
	defer server.Close()

	mon := sync.Monitor{}
	conf := NovaConf{Availability: "public"}

	api := newNovaAPI(mon, k, conf).(*novaAPI)
	api.Init(t.Context())

	ctx := context.Background()
	servers, err := api.GetAllServers(ctx)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if len(servers) != 1 {
		t.Fatalf("expected 1 server, got %d", len(servers))
	}
}

func TestNovaAPI_GetAllHypervisors(t *testing.T) {
	handler := func(w http.ResponseWriter, r *http.Request) {
		w.Header().Add("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		resp := struct {
			Hypervisors []Hypervisor `json:"hypervisors"`
		}{
			Hypervisors: []Hypervisor{{ID: 1, Hostname: "hypervisor1"}},
		}
		if err := json.NewEncoder(w).Encode(resp); err != nil {
			t.Fatalf("failed to write response: %v", err)
		}
	}
	server, k := setupNovaMockServer(handler)
	defer server.Close()

	mon := sync.Monitor{}
	conf := NovaConf{Availability: "public"}

	api := newNovaAPI(mon, k, conf).(*novaAPI)
	api.Init(t.Context())

	ctx := context.Background()
	hypervisors, err := api.GetAllHypervisors(ctx)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if len(hypervisors) != 1 {
		t.Fatalf("expected 1 hypervisor, got %d", len(hypervisors))
	}
}

func TestNovaAPI_GetAllFlavors(t *testing.T) {
	handler := func(w http.ResponseWriter, r *http.Request) {
		w.Header().Add("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		resp := struct {
			Flavors []Flavor `json:"flavors"`
		}{
			Flavors: []Flavor{{ID: "1", Name: "flavor1"}},
		}
		if err := json.NewEncoder(w).Encode(resp); err != nil {
			t.Fatalf("failed to write response: %v", err)
		}
	}
	server, k := setupNovaMockServer(handler)
	defer server.Close()

	mon := sync.Monitor{}
	conf := NovaConf{Availability: "public"}

	api := newNovaAPI(mon, k, conf).(*novaAPI)
	api.Init(t.Context())

	ctx := context.Background()
	flavors, err := api.GetAllFlavors(ctx)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if len(flavors) != 1 {
		t.Fatalf("expected 1 flavor, got %d", len(flavors))
	}
}
