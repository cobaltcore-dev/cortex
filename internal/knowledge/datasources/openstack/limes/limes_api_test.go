// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package limes

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/cobaltcore-dev/cortex/api/v1alpha1"
	"github.com/cobaltcore-dev/cortex/internal/knowledge/datasources"
	"github.com/cobaltcore-dev/cortex/internal/knowledge/datasources/openstack/identity"
	"github.com/cobaltcore-dev/cortex/pkg/keystone"
	testlibKeystone "github.com/cobaltcore-dev/cortex/pkg/keystone/testing"
)

func setupLimesMockServer(handler http.HandlerFunc) (*httptest.Server, keystone.KeystoneAPI) {
	server := httptest.NewServer(handler)
	return server, &testlibKeystone.MockKeystoneAPI{Url: server.URL + "/"}
}

func TestNewLimesAPI(t *testing.T) {
	mon := datasources.Monitor{}
	k := &testlibKeystone.MockKeystoneAPI{}
	conf := v1alpha1.LimesDatasource{}

	api := NewLimesAPI(mon, k, conf)
	if api == nil {
		t.Fatal("expected non-nil api")
	}
}

func TestLimesAPI_GetAllCommitments(t *testing.T) {
	handler := func(w http.ResponseWriter, r *http.Request) {
		w.Header().Add("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		if _, err := w.Write([]byte(`{"commitments": [{"id": 1, "uuid": "test-uuid", "service_type": "compute", "resource_name": "cores", "availability_zone": "az1", "amount": 10, "unit": "cores", "duration": "1 year", "created_at": 1640995200, "expires_at": 1672531200, "transferable": false, "notify_on_confirm": false}]}`)); err != nil {
			t.Fatalf("failed to write response: %v", err)
		}
	}
	server, k := setupLimesMockServer(handler)
	defer server.Close()

	mon := datasources.Monitor{}
	conf := v1alpha1.LimesDatasource{}

	api := NewLimesAPI(mon, k, conf).(*limesAPI)
	if err := api.Init(t.Context()); err != nil {
		t.Fatalf("failed to init cinder api: %v", err)
	}

	ctx := t.Context()
	projects := []identity.Project{{ID: "project1", DomainID: "domain1"}}
	commitments, err := api.GetAllCommitments(ctx, projects)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if len(commitments) != 1 {
		t.Fatalf("expected 1 commitment, got %d", len(commitments))
	}
	commitment := commitments[0]
	if commitment.ID != 1 || commitment.UUID != "test-uuid" || commitment.ServiceType != "compute" {
		t.Errorf("unexpected commitment: %+v", commitment)
	}
	// Check the project and domain IDs
	if commitment.ProjectID != "project1" || commitment.DomainID != "domain1" {
		t.Errorf("expected project ID 'project1' and domain ID 'domain1', got project ID '%s' and domain ID '%s'", commitment.ProjectID, commitment.DomainID)
	}
}

func TestLimesAPI_GetAllCommitments_Error(t *testing.T) {
	handler := func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		if _, err := w.Write([]byte(`{"error": "internal server error"}`)); err != nil {
			t.Fatalf("failed to write response: %v", err)
		}
	}
	server, k := setupLimesMockServer(handler)
	defer server.Close()

	mon := datasources.Monitor{}
	conf := v1alpha1.LimesDatasource{}

	api := NewLimesAPI(mon, k, conf).(*limesAPI)
	if err := api.Init(t.Context()); err != nil {
		t.Fatalf("failed to init cinder api: %v", err)
	}

	ctx := t.Context()
	projects := []identity.Project{{ID: "project1", DomainID: "domain1"}}
	_, err := api.GetAllCommitments(ctx, projects)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
}

func TestLimesAPI_GetAllCommitments_EmptyResponse(t *testing.T) {
	handler := func(w http.ResponseWriter, r *http.Request) {
		w.Header().Add("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		if _, err := w.Write([]byte(`{"commitments": []}`)); err != nil {
			t.Fatalf("failed to write response: %v", err)
		}
	}
	server, k := setupLimesMockServer(handler)
	defer server.Close()

	mon := datasources.Monitor{}
	conf := v1alpha1.LimesDatasource{}

	api := NewLimesAPI(mon, k, conf).(*limesAPI)
	if err := api.Init(t.Context()); err != nil {
		t.Fatalf("failed to init cinder api: %v", err)
	}

	ctx := t.Context()
	projects := []identity.Project{{ID: "project1", DomainID: "domain1"}}
	commitments, err := api.GetAllCommitments(ctx, projects)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if len(commitments) != 0 {
		t.Fatalf("expected 0 commitments, got %d", len(commitments))
	}
}

func TestLimesAPI_GetAllCommitments_MultipleProjects(t *testing.T) {
	handler := http.NewServeMux()

	handler.HandleFunc("/v1/domains/domain1/projects/project1/commitments", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Add("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, err := w.Write([]byte(`{"commitments": [{"id": 1, "uuid": "uuid1", "service_type": "compute", "resource_name": "cores", "availability_zone": "az1", "amount": 10, "unit": "cores", "duration": "1 year", "created_at": 1640995200, "expires_at": 1672531200, "transferable": false, "notify_on_confirm": false}]}`))
		if err != nil {
			t.Fatalf("failed to write response: %v", err)
		}
	})

	handler.HandleFunc("/v1/domains/domain1/projects/project2/commitments", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Add("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, err := w.Write([]byte(`{"commitments": [{"id": 2, "uuid": "uuid2", "service_type": "storage", "resource_name": "capacity", "availability_zone": "az2", "amount": 100, "unit": "GiB", "duration": "6 months", "created_at": 1640995200, "expires_at": 1672531200, "transferable": false, "notify_on_confirm": false}]}`))
		if err != nil {
			t.Fatalf("failed to write response: %v", err)
		}
	})

	server := httptest.NewServer(handler)
	defer server.Close()

	mon := datasources.Monitor{}
	conf := v1alpha1.LimesDatasource{}
	k := &testlibKeystone.MockKeystoneAPI{Url: server.URL + "/"}

	api := NewLimesAPI(mon, k, conf).(*limesAPI)
	if err := api.Init(t.Context()); err != nil {
		t.Fatalf("failed to init cinder api: %v", err)
	}

	ctx := t.Context()
	projects := []identity.Project{
		{ID: "project1", DomainID: "domain1"},
		{ID: "project2", DomainID: "domain1"},
	}
	commitments, err := api.GetAllCommitments(ctx, projects)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if len(commitments) != 2 {
		t.Fatalf("expected 2 commitments, got %d", len(commitments))
	}
}
