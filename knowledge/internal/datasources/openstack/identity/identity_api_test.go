// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package identity

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/cobaltcore-dev/cortex/knowledge/api/datasources/openstack/identity"
	"github.com/cobaltcore-dev/cortex/knowledge/api/v1alpha1"
	"github.com/cobaltcore-dev/cortex/knowledge/internal/datasources"
	"github.com/cobaltcore-dev/cortex/lib/keystone"

	testlibKeystone "github.com/cobaltcore-dev/cortex/testlib/keystone"
)

func setupIdentityMockServer(handler http.HandlerFunc) (*httptest.Server, keystone.KeystoneAPI) {
	server := httptest.NewServer(handler)
	return server, &testlibKeystone.MockKeystoneAPI{Url: server.URL + "/"}
}

func TestIdentityAPI_GetAllDomains(t *testing.T) {
	handler := func(w http.ResponseWriter, r *http.Request) {
		w.Header().Add("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		resp := struct {
			Domains []identity.Domain `json:"domains"`
		}{
			Domains: []identity.Domain{
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

	mon := datasources.Monitor{}
	conf := v1alpha1.IdentityDatasource{Type: v1alpha1.IdentityDatasourceTypeDomains}

	api := NewIdentityAPI(mon, k, conf).(*identityAPI)
	if err := api.Init(t.Context()); err != nil {
		t.Fatalf("failed to init cinder api: %v", err)
	}

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
		w.Header().Add("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		resp := struct {
			Projects []struct {
				ID       string   `json:"id"`
				Name     string   `json:"name"`
				DomainID string   `json:"domain_id"`
				IsDomain bool     `json:"is_domain"`
				Enabled  bool     `json:"enabled"`
				Tags     []string `json:"tags"`
			} `json:"projects"`
		}{
			Projects: []struct {
				ID       string   `json:"id"`
				Name     string   `json:"name"`
				DomainID string   `json:"domain_id"`
				IsDomain bool     `json:"is_domain"`
				Enabled  bool     `json:"enabled"`
				Tags     []string `json:"tags"`
			}{
				{ID: "1", Name: "project1", DomainID: "domain1", IsDomain: false, Enabled: true, Tags: []string{"foo", "bar"}},
				{ID: "2", Name: "project2", DomainID: "domain2", IsDomain: false, Enabled: true, Tags: []string{}},
				{ID: "3", Name: "project3", DomainID: "domain3", IsDomain: false, Enabled: true, Tags: []string{"foo"}},
			},
		}
		if err := json.NewEncoder(w).Encode(resp); err != nil {
			t.Fatalf("failed to write response: %v", err)
		}
	}
	server, k := setupIdentityMockServer(handler)
	defer server.Close()

	mon := datasources.Monitor{}
	conf := v1alpha1.IdentityDatasource{Type: v1alpha1.IdentityDatasourceTypeProjects}

	api := NewIdentityAPI(mon, k, conf).(*identityAPI)
	if err := api.Init(t.Context()); err != nil {
		t.Fatalf("failed to init cinder api: %v", err)
	}

	ctx := t.Context()
	projects, err := api.GetAllProjects(ctx)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	amountProjects := len(projects)
	if amountProjects != 3 {
		t.Fatalf("expected 2 projects, got %d", amountProjects)
	}

	if projects[0].Tags != "foo,bar" {
		t.Errorf("expected tags to be 'foo,bar', got '%s'", projects[0].Tags)
	}
	if projects[1].Tags != "" {
		t.Errorf("expected tags to be '', got '%s'", projects[1].Tags)
	}
	if projects[2].Tags != "foo" {
		t.Errorf("expected tags to be 'foo', got '%s'", projects[2].Tags)
	}
}
