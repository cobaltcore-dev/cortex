// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package nova

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/cobaltcore-dev/cortex/knowledge/api/datasources/openstack/nova"
	"github.com/cobaltcore-dev/cortex/knowledge/api/v1alpha1"
	"github.com/cobaltcore-dev/cortex/knowledge/internal/datasources"
	"github.com/cobaltcore-dev/cortex/pkg/keystone"
	testlibKeystone "github.com/cobaltcore-dev/cortex/pkg/keystone/testing"
)

func setupNovaMockServer(handler http.HandlerFunc) (*httptest.Server, keystone.KeystoneAPI) {
	server := httptest.NewServer(handler)
	return server, &testlibKeystone.MockKeystoneAPI{Url: server.URL + "/"}
}

func TestNewNovaAPI(t *testing.T) {
	mon := datasources.Monitor{}
	k := &testlibKeystone.MockKeystoneAPI{}
	conf := v1alpha1.NovaDatasource{}

	api := NewNovaAPI(mon, k, conf)
	if api == nil {
		t.Fatal("expected non-nil api")
	}
}

func TestNovaAPI_GetDeletedServers(t *testing.T) {
	tests := []struct {
		Name string
		Time time.Time
	}{
		{
			Name: "should find default changes-since of 6 hours",
			Time: time.Now().Add(-6 * time.Hour),
		},
		{
			Name: "should find custom changes-since of 1 hour",
			Time: time.Now().Add(-1 * time.Hour),
		},
	}
	for _, tt := range tests {
		handler := func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Query().Get("changes-since") != tt.Time.Format(time.RFC3339) {
				t.Fatalf("expected changes-since query parameter to be %s, got %s", tt.Time.Format(time.RFC3339), r.URL.Query().Get("changes-since"))
			}
			w.Header().Add("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			if _, err := w.Write([]byte(`{"servers": [{
				"id": "1",
				"name": "server1",
				"status": "DELETED",
				"flavor": {"id": "1", "name": "flavor1"}
			}]}`)); err != nil {
				t.Fatalf("failed to write response: %v", err)
			}
		}
		server, k := setupNovaMockServer(handler)
		defer server.Close()

		mon := datasources.Monitor{}
		conf := v1alpha1.NovaDatasource{Type: v1alpha1.NovaDatasourceTypeServers}

		api := NewNovaAPI(mon, k, conf).(*novaAPI)
		if err := api.Init(t.Context()); err != nil {
			t.Fatalf("failed to init cinder api: %v", err)
		}

		ctx := t.Context()
		servers, err := api.GetDeletedServers(ctx, tt.Time)
		if err != nil {
			t.Fatalf("expected no error, got %v", err)
		}
		if len(servers) != 1 {
			t.Fatalf("expected 1 server, got %d", len(servers))
		}
	}
}

func TestNovaAPI_GetAllServers(t *testing.T) {
	handler := func(w http.ResponseWriter, r *http.Request) {
		// changes-since is not supported by the hypervisor api so
		// the query parameter should not be set.
		if r.URL.Query().Get("changes-since") != "" {
			t.Fatalf("expected no changes-since query parameter, got %s", r.URL.Query().Get("changes-since"))
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

	mon := datasources.Monitor{}
	conf := v1alpha1.NovaDatasource{Type: v1alpha1.NovaDatasourceTypeServers}

	api := NewNovaAPI(mon, k, conf).(*novaAPI)
	if err := api.Init(t.Context()); err != nil {
		t.Fatalf("failed to init cinder api: %v", err)
	}

	ctx := t.Context()
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
		// changes-since is not supported by the hypervisor api so
		// the query parameter should not be set.
		if r.URL.Query().Get("changes-since") != "" {
			t.Fatalf("expected no changes-since query parameter, got %s", r.URL.Query().Get("changes-since"))
		}
		w.Header().Add("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		resp := struct {
			Hypervisors []nova.Hypervisor `json:"hypervisors"`
		}{
			Hypervisors: []nova.Hypervisor{{ID: "1", Hostname: "hypervisor1", CPUInfo: "{}"}},
		}
		if err := json.NewEncoder(w).Encode(resp); err != nil {
			t.Fatalf("failed to write response: %v", err)
		}
	}
	server, k := setupNovaMockServer(handler)
	defer server.Close()

	mon := datasources.Monitor{}
	conf := v1alpha1.NovaDatasource{Type: v1alpha1.NovaDatasourceTypeHypervisors}

	api := NewNovaAPI(mon, k, conf).(*novaAPI)
	if err := api.Init(t.Context()); err != nil {
		t.Fatalf("failed to init cinder api: %v", err)
	}

	ctx := t.Context()
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
		// We only want the current state of all flavors, so
		// the changes-since query parameter should not be set.
		if r.URL.Query().Get("changes-since") != "" {
			t.Fatalf("expected no changes-since query parameter, got %s", r.URL.Query().Get("changes-since"))
		}
		w.Header().Add("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		resp := struct {
			Flavors []nova.Flavor `json:"flavors"`
		}{
			Flavors: []nova.Flavor{{ID: "1", Name: "flavor1"}},
		}
		if err := json.NewEncoder(w).Encode(resp); err != nil {
			t.Fatalf("failed to write response: %v", err)
		}
	}
	server, k := setupNovaMockServer(handler)
	defer server.Close()

	mon := datasources.Monitor{}
	conf := v1alpha1.NovaDatasource{Type: v1alpha1.NovaDatasourceTypeFlavors}

	api := NewNovaAPI(mon, k, conf).(*novaAPI)
	if err := api.Init(t.Context()); err != nil {
		t.Fatalf("failed to init cinder api: %v", err)
	}

	ctx := t.Context()
	flavors, err := api.GetAllFlavors(ctx)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if len(flavors) != 1 {
		t.Fatalf("expected 1 flavor, got %d", len(flavors))
	}
}

func TestNovaAPI_GetAllMigrations(t *testing.T) {
	handler := func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("changes-since") != "" {
			t.Fatalf("expected no changes-since query parameter, got %s", r.URL.Query().Get("changes-since"))
		}
		w.Header().Add("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		resp := struct {
			Migrations []nova.Migration `json:"migrations"`
			Links      []struct {
				Rel  string `json:"rel"`
				Href string `json:"href"`
			} `json:"migrations_links"`
		}{
			Migrations: []nova.Migration{{ID: 1, SourceCompute: "host1", DestCompute: "host2", Status: "completed"}},
		}
		if err := json.NewEncoder(w).Encode(resp); err != nil {
			t.Fatalf("failed to write response: %v", err)
		}
	}

	server, k := setupNovaMockServer(handler)
	defer server.Close()

	mon := datasources.Monitor{}
	conf := v1alpha1.NovaDatasource{Type: v1alpha1.NovaDatasourceTypeMigrations}

	api := NewNovaAPI(mon, k, conf).(*novaAPI)
	if err := api.Init(t.Context()); err != nil {
		t.Fatalf("failed to init cinder api: %v", err)
	}

	ctx := t.Context()
	migrations, err := api.GetAllMigrations(ctx)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if len(migrations) != 1 {
		t.Fatalf("expected 1 migration, got %d", len(migrations))
	}
	if migrations[0].ID != 1 || migrations[0].SourceCompute != "host1" || migrations[0].DestCompute != "host2" || migrations[0].Status != "completed" {
		t.Errorf("unexpected migration data: %+v", migrations[0])
	}
}
