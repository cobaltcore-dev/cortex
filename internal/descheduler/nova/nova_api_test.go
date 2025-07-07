// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package nova

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/cobaltcore-dev/cortex/internal/conf"
	"github.com/cobaltcore-dev/cortex/internal/keystone"
	testlibKeystone "github.com/cobaltcore-dev/cortex/testlib/keystone"
)

type mockNovaAPI struct {
	InitFunc        func(ctx context.Context)
	GetFunc         func(ctx context.Context, id string) (server, error)
	LiveMigrateFunc func(ctx context.Context, id string) error
}

func (m *mockNovaAPI) Init(ctx context.Context) {
	if m.InitFunc != nil {
		m.InitFunc(ctx)
	}
}

func (m *mockNovaAPI) Get(ctx context.Context, id string) (server, error) {
	if m.GetFunc != nil {
		return m.GetFunc(ctx, id)
	}
	return server{}, nil
}

func (m *mockNovaAPI) LiveMigrate(ctx context.Context, id string) error {
	if m.LiveMigrateFunc != nil {
		return m.LiveMigrateFunc(ctx, id)
	}
	return nil
}

func setupNovaMockServer(handler http.HandlerFunc) (*httptest.Server, keystone.KeystoneAPI) {
	server := httptest.NewServer(handler)
	return server, &testlibKeystone.MockKeystoneAPI{Url: server.URL + "/"}
}

func TestNewNovaAPI(t *testing.T) {
	k := &testlibKeystone.MockKeystoneAPI{}
	conf := conf.NovaDeschedulerConfig{}

	api := NewNovaAPI(k, conf)
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
	conf := conf.NovaDeschedulerConfig{Availability: "public"}
	nova := NewNovaAPI(k, conf).(*novaAPI)
	ctx := t.Context()
	nova.Init(ctx)

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
	conf := conf.NovaDeschedulerConfig{Availability: "public"}
	nova := NewNovaAPI(k, conf).(*novaAPI)
	ctx := t.Context()
	nova.Init(ctx)

	err := nova.LiveMigrate(ctx, "server-123")
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
}
