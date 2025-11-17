// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package nova

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/cobaltcore-dev/cortex/pkg/keystone"
	testlibKeystone "github.com/cobaltcore-dev/cortex/pkg/keystone/testing"
	"github.com/gophercloud/gophercloud/v2"
)

func setupNovaMockServer(handler http.HandlerFunc) (*httptest.Server, keystone.KeystoneAPI) {
	server := httptest.NewServer(handler)
	return server, &testlibKeystone.MockKeystoneAPI{Url: server.URL + "/"}
}

func TestNewNovaAPI(t *testing.T) {
	api := NewNovaAPI()
	if api == nil {
		t.Fatal("expected non-nil api")
	}
}

func TestNovaAPI_GetServer(t *testing.T) {
	handler := func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Fatalf("expected GET method, got %s", r.Method)
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, err := w.Write([]byte(`{"server": {"id": "server-123", "status": "ACTIVE", "OS-EXT-SRV-ATTR:host": "host-1"}}`))
		if err != nil {
			t.Fatalf("failed to write response: %v", err)
		}
	}
	server, k := setupNovaMockServer(handler)
	defer server.Close()
	nova := novaAPI{}
	nova.sc = &gophercloud.ServiceClient{
		ProviderClient: k.Client(),
		Endpoint:       server.URL + "/",
		Type:           "compute",
		Microversion:   "2.53",
	}
	ctx := t.Context()

	s, err := nova.Get(ctx, "server-123")
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if s.ID != "server-123" || s.Status != "ACTIVE" || s.ComputeHost != "host-1" {
		t.Errorf("unexpected server data: %+v", s)
	}
}

func TestNovaAPI_LiveMigrate(t *testing.T) {
	handler := func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Fatalf("expected POST method, got %s", r.Method)
		}
		w.WriteHeader(http.StatusAccepted)
	}
	server, k := setupNovaMockServer(handler)
	defer server.Close()
	nova := novaAPI{}
	nova.sc = &gophercloud.ServiceClient{
		ProviderClient: k.Client(),
		Endpoint:       server.URL + "/",
		Type:           "compute",
		Microversion:   "2.53",
	}
	ctx := t.Context()

	err := nova.LiveMigrate(ctx, "server-123")
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
}

func TestNovaAPI_GetServerMigrations(t *testing.T) {
	migrationsResponse := `{"migrations": [
	{"instance_uuid": "server-123", "source_compute": "host-1", "dest_compute": "host-2"},
	{"instance_uuid": "server-123", "source_compute": "host-2", "dest_compute": "host-3"}
], "migrations_links": []}`

	handler := func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Fatalf("expected GET method, got %s", r.Method)
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, err := w.Write([]byte(migrationsResponse))
		if err != nil {
			t.Fatalf("failed to write response: %v", err)
		}
	}
	server, k := setupNovaMockServer(handler)
	defer server.Close()
	nova := novaAPI{}
	nova.sc = &gophercloud.ServiceClient{
		ProviderClient: k.Client(),
		Endpoint:       server.URL + "/",
		Type:           "compute",
		Microversion:   "2.53",
	}
	ctx := t.Context()

	migrations, err := nova.GetServerMigrations(ctx, "server-123")
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if len(migrations) != 2 {
		t.Fatalf("expected 2 migrations, got %d", len(migrations))
	}
	if migrations[0].InstanceUUID != "server-123" || migrations[0].SourceCompute != "host-1" || migrations[0].DestCompute != "host-2" {
		t.Errorf("unexpected first migration: %+v", migrations[0])
	}
	if migrations[1].InstanceUUID != "server-123" || migrations[1].SourceCompute != "host-2" || migrations[1].DestCompute != "host-3" {
		t.Errorf("unexpected second migration: %+v", migrations[1])
	}
}
