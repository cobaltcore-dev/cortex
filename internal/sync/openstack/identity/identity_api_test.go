// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package identity

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/cobaltcore-dev/cortex/internal/keystone"
	"github.com/cobaltcore-dev/cortex/internal/sync"

	testlibKeystone "github.com/cobaltcore-dev/cortex/testlib/keystone"
)

func setupIdentityMockServer(handler http.HandlerFunc) (*httptest.Server, keystone.KeystoneAPI) {
	server := httptest.NewServer(handler)
	return server, &testlibKeystone.MockKeystoneAPI{Url: server.URL + "/"}
}

func TestIdentityAPI_GetAllDomains(t *testing.T) {
	handler := func(w http.ResponseWriter, r *http.Request) {
		// changes-since is not supported by the hypervisor api so
		// the query parameter should not be set.
		if r.URL.Query().Get("changes-since") != "" {
			t.Fatalf("expected no changes-since query parameter, got %s", r.URL.Query().Get("changes-since"))
		}
		w.Header().Add("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		resp := struct {
			Domains []Domain `json:"domains"`
		}{
			Domains: []Domain{
				{ID: "1", Name: "domain1", Enabled: true},
				{ID: "2", Name: "domain2", Enabled: true},
			},
		}
		if err := json.NewEncoder(w).Encode(resp); err != nil {
			t.Fatalf("failed to write response: %v", err)
		}
	}
	server, k := setupIdentityMockServer(handler)
	defer server.Close()

	mon := sync.Monitor{}
	conf := IdentityConf{Availability: "public"}

	api := NewIdentityAPI(mon, k, conf).(*identityAPI)
	api.Init(t.Context())

	ctx := t.Context()
	domains, err := api.GetAllDomains(ctx)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	amountDomains := len(domains)
	if amountDomains != 2 {
		t.Fatalf("expected 2 domains, got %d", amountDomains)
	}
}

func TestIdentityAPI_GetAllProjects(t *testing.T) {
	handler := func(w http.ResponseWriter, r *http.Request) {
		// changes-since is not supported by the hypervisor api so
		// the query parameter should not be set.
		if r.URL.Query().Get("changes-since") != "" {
			t.Fatalf("expected no changes-since query parameter, got %s", r.URL.Query().Get("changes-since"))
		}
		w.Header().Add("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		resp := struct {
			Projects []Project `json:"projects"`
		}{
			Projects: []Project{
				{ID: "1", Name: "project1", DomainID: "domain1"},
				{ID: "2", Name: "project2", DomainID: "domain2"},
			},
		}
		if err := json.NewEncoder(w).Encode(resp); err != nil {
			t.Fatalf("failed to write response: %v", err)
		}
	}
	server, k := setupIdentityMockServer(handler)
	defer server.Close()

	mon := sync.Monitor{}
	conf := IdentityConf{Availability: "public"}

	api := NewIdentityAPI(mon, k, conf).(*identityAPI)
	api.Init(t.Context())

	ctx := t.Context()
	projects, err := api.GetAllProjects(ctx)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	amountProjects := len(projects)
	if amountProjects != 2 {
		t.Fatalf("expected 2 projects, got %d", amountProjects)
	}
}
