// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package openstack

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

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
	tests := []struct {
		name string
		time *time.Time
	}{
		{"nil", nil},
		{"time", &time.Time{}},
	}
	for _, tt := range tests {
		handler := func(w http.ResponseWriter, r *http.Request) {
			if tt.time == nil {
				// Check that the changes-since query parameter is not set.
				if r.URL.Query().Get("changes-since") != "" {
					t.Fatalf("expected no changes-since query parameter, got %s", r.URL.Query().Get("changes-since"))
				}
			} else {
				if r.URL.Query().Get("changes-since") != tt.time.Format(time.RFC3339) {
					t.Fatalf("expected changes-since query parameter to be %s, got %s", tt.time.Format(time.RFC3339), r.URL.Query().Get("changes-since"))
				}
			}
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

		ctx := t.Context()
		servers, err := api.GetAllServers(ctx, tt.time)
		if err != nil {
			t.Fatalf("expected no error, got %v", err)
		}
		if len(servers) != 1 {
			t.Fatalf("expected 1 server, got %d", len(servers))
		}
	}
}

func TestNovaAPI_GetAllHypervisors(t *testing.T) {
	tests := []struct {
		name string
		time *time.Time
	}{
		{"nil", nil},
		{"time", &time.Time{}},
	}
	for _, tt := range tests {
		handler := func(w http.ResponseWriter, r *http.Request) {
			// changes-since is not supported by the hypervisor api so
			// the query parameter should not be set.
			if r.URL.Query().Get("changes-since") != "" {
				t.Fatalf("expected no changes-since query parameter, got %s", r.URL.Query().Get("changes-since"))
			}
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

		ctx := t.Context()
		hypervisors, err := api.GetAllHypervisors(ctx, tt.time)
		if err != nil {
			t.Fatalf("expected no error, got %v", err)
		}
		if len(hypervisors) != 1 {
			t.Fatalf("expected 1 hypervisor, got %d", len(hypervisors))
		}
	}
}

func TestNovaAPI_GetAllFlavors(t *testing.T) {
	tests := []struct {
		name string
		time *time.Time
	}{
		{"nil", nil},
		{"time", &time.Time{}},
	}
	for _, tt := range tests {
		handler := func(w http.ResponseWriter, r *http.Request) {
			if tt.time == nil {
				// Check that the changes-since query parameter is not set.
				if r.URL.Query().Get("changes-since") != "" {
					t.Fatalf("expected no changes-since query parameter, got %s", r.URL.Query().Get("changes-since"))
				}
			} else {
				if r.URL.Query().Get("changes-since") != tt.time.Format(time.RFC3339) {
					t.Fatalf("expected changes-since query parameter to be %s, got %s", tt.time.Format(time.RFC3339), r.URL.Query().Get("changes-since"))
				}
			}
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

		ctx := t.Context()
		flavors, err := api.GetAllFlavors(ctx, tt.time)
		if err != nil {
			t.Fatalf("expected no error, got %v", err)
		}
		if len(flavors) != 1 {
			t.Fatalf("expected 1 flavor, got %d", len(flavors))
		}
	}
}
