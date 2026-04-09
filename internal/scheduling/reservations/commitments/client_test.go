// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package commitments

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gophercloud/gophercloud/v2"
)

func TestNewCommitmentsClient(t *testing.T) {
	client := NewCommitmentsClient()
	if client == nil {
		t.Fatal("expected client to be created, got nil")
	}

	// Check that the returned client is of the correct type
	_, ok := client.(*commitmentsClient)
	if !ok {
		t.Fatal("expected client to be of type *commitmentsClient")
	}
}

func TestCommitmentsClient_ListProjects(t *testing.T) {
	// Mock server for Keystone identity service
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v3/projects" {
			// Return raw JSON string as the gophercloud pages expect
			w.Header().Set("Content-Type", "application/json")
			_, err := w.Write([]byte(`{
				"projects": [
					{
						"id": "project1",
						"name": "Test Project 1",
						"domain_id": "domain1",
						"parent_id": ""
					},
					{
						"id": "project2",
						"name": "Test Project 2",
						"domain_id": "domain1",
						"parent_id": "project1"
					}
				]
			}`))
			if err != nil {
				t.Fatalf("failed to write response: %v", err)
			}
			return
		}
		http.NotFound(w, r)
	}))
	defer server.Close()

	client := &commitmentsClient{
		keystone: &gophercloud.ServiceClient{
			ProviderClient: &gophercloud.ProviderClient{
				HTTPClient: *http.DefaultClient,
			},
			Endpoint: server.URL + "/v3/",
		},
	}

	ctx := context.Background()
	projects, err := client.ListProjects(ctx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	expectedProjects := []Project{
		{
			ID:       "project1",
			Name:     "Test Project 1",
			DomainID: "domain1",
			ParentID: "",
		},
		{
			ID:       "project2",
			Name:     "Test Project 2",
			DomainID: "domain1",
			ParentID: "project1",
		},
	}

	if len(projects) != len(expectedProjects) {
		t.Fatalf("expected %d projects, got %d", len(expectedProjects), len(projects))
	}

	for i, expected := range expectedProjects {
		if projects[i] != expected {
			t.Errorf("project %d: expected %+v, got %+v", i, expected, projects[i])
		}
	}
}

func TestCommitmentsClient_ListProjects_Error(t *testing.T) {
	// Mock server that returns an error
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
	}))
	defer server.Close()

	client := &commitmentsClient{
		keystone: &gophercloud.ServiceClient{
			ProviderClient: &gophercloud.ProviderClient{
				HTTPClient: *http.DefaultClient,
			},
			Endpoint: server.URL + "/v3",
		},
	}

	ctx := context.Background()
	projects, err := client.ListProjects(ctx)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if projects != nil {
		t.Errorf("expected nil projects, got %+v", projects)
	}
}

func TestCommitmentsClient_ListCommitmentsByID(t *testing.T) {
	// Mock server for Limes service
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Extract project and domain from URL path
		pathParts := strings.Split(r.URL.Path, "/")
		if len(pathParts) >= 6 && pathParts[len(pathParts)-1] == "commitments" {
			projectID := pathParts[len(pathParts)-2]
			domainID := pathParts[len(pathParts)-4]

			response := map[string]any{
				"commitments": []Commitment{
					{
						ID:               1,
						UUID:             "commitment1",
						ServiceType:      "compute",
						ResourceName:     "instances",
						AvailabilityZone: "nova",
						Amount:           10,
						Unit:             "instances",
						Duration:         "1 year",
						//nolint:gosec
						CreatedAt: uint64(time.Now().Unix()),
						//nolint:gosec
						ExpiresAt: uint64(time.Now().Add(365 * 24 * time.Hour).Unix()),
						Status:    "confirmed",
						ProjectID: projectID,
						DomainID:  domainID,
					},
				},
			}
			err := json.NewEncoder(w).Encode(response)
			if err != nil {
				t.Fatalf("failed to write response: %v", err)
			}
			return
		}
		http.NotFound(w, r)
	}))
	defer server.Close()

	client := &commitmentsClient{
		limes: &gophercloud.ServiceClient{
			ProviderClient: &gophercloud.ProviderClient{
				HTTPClient: *http.DefaultClient,
				TokenID:    "test-token",
			},
			Endpoint: server.URL + "/",
		},
	}

	projects := []Project{
		{
			ID:       "project1",
			DomainID: "domain1",
			Name:     "Test Project",
		},
	}

	ctx := context.Background()
	commitments, err := client.ListCommitmentsByID(ctx, projects...)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(commitments) != 1 {
		t.Fatalf("expected 1 commitment, got %d", len(commitments))
	}

	commitment, exists := commitments["commitment1"]
	if !exists {
		t.Fatal("expected commitment1 to exist")
	}

	if commitment.UUID != "commitment1" {
		t.Errorf("expected UUID commitment1, got %s", commitment.UUID)
	}
	if commitment.ProjectID != "project1" {
		t.Errorf("expected ProjectID project1, got %s", commitment.ProjectID)
	}
	if commitment.DomainID != "domain1" {
		t.Errorf("expected DomainID domain1, got %s", commitment.DomainID)
	}
	if commitment.ServiceType != "compute" {
		t.Errorf("expected ServiceType compute, got %s", commitment.ServiceType)
	}
}

func TestCommitmentsClient_listCommitments(t *testing.T) {
	// Mock server for Limes service
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasSuffix(r.URL.Path, "/commitments") {
			http.NotFound(w, r)
			return
		}

		// Check auth token
		token := r.Header.Get("X-Auth-Token")
		if token != "test-token" {
			http.Error(w, "Unauthorized", http.StatusUnauthorized)
			return
		}

		response := map[string]any{
			"commitments": []Commitment{
				{
					ID:               1,
					UUID:             "commitment1",
					ServiceType:      "compute",
					ResourceName:     "instances",
					AvailabilityZone: "nova",
					Amount:           5,
					Unit:             "instances",
					Duration:         "6 months",
					CreatedAt:        1672531200, // 2023-01-01
					ExpiresAt:        1688169600, // 2023-07-01
					Status:           "confirmed",
				},
			},
		}
		if err := json.NewEncoder(w).Encode(response); err != nil {
			t.Fatalf("failed to write response: %v", err)
		}
	}))
	defer server.Close()

	client := &commitmentsClient{
		limes: &gophercloud.ServiceClient{
			ProviderClient: &gophercloud.ProviderClient{
				HTTPClient: *http.DefaultClient,
				TokenID:    "test-token",
			},
			Endpoint: server.URL + "/",
		},
	}

	project := Project{
		ID:       "test-project",
		DomainID: "test-domain",
		Name:     "Test Project",
	}

	ctx := context.Background()
	commitments, err := client.listCommitments(ctx, project)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(commitments) != 1 {
		t.Fatalf("expected 1 commitment, got %d", len(commitments))
	}

	commitment := commitments[0]
	if commitment.UUID != "commitment1" {
		t.Errorf("expected commitment UUID commitment1, got %s", commitment.UUID)
	}
	if commitment.ProjectID != "test-project" {
		t.Errorf("expected ProjectID test-project, got %s", commitment.ProjectID)
	}
	if commitment.DomainID != "test-domain" {
		t.Errorf("expected DomainID test-domain, got %s", commitment.DomainID)
	}
}

func TestCommitmentsClient_listCommitments_HTTPError(t *testing.T) {
	// Mock server that returns non-200 status
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "Not Found", http.StatusNotFound)
	}))
	defer server.Close()

	client := &commitmentsClient{
		limes: &gophercloud.ServiceClient{
			ProviderClient: &gophercloud.ProviderClient{
				HTTPClient: *http.DefaultClient,
				TokenID:    "test-token",
			},
			Endpoint: server.URL + "/",
		},
	}

	project := Project{ID: "test-project", DomainID: "test-domain"}

	ctx := context.Background()
	commitments, err := client.listCommitments(ctx, project)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if commitments != nil {
		t.Errorf("expected nil commitments, got %+v", commitments)
	}

	// Gophercloud returns a more detailed error message
	if !strings.Contains(err.Error(), "404") {
		t.Errorf("expected error to contain '404', got %q", err.Error())
	}
}

func TestCommitmentsClient_listCommitments_JSONError(t *testing.T) {
	// Mock server that returns invalid JSON
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		if _, err := w.Write([]byte("invalid json")); err != nil {
			t.Fatalf("failed to write response: %v", err)
		}
	}))
	defer server.Close()

	client := &commitmentsClient{
		limes: &gophercloud.ServiceClient{
			ProviderClient: &gophercloud.ProviderClient{
				HTTPClient: *http.DefaultClient,
				TokenID:    "test-token",
			},
			Endpoint: server.URL,
		},
	}

	project := Project{ID: "test-project", DomainID: "test-domain"}

	ctx := context.Background()
	commitments, err := client.listCommitments(ctx, project)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if commitments != nil {
		t.Errorf("expected nil commitments, got %+v", commitments)
	}
}

func TestCommitmentsClient_ContextCancellation(t *testing.T) {
	// Test context cancellation handling
	slowServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Simulate a slow server
		time.Sleep(100 * time.Millisecond)
		if err := json.NewEncoder(w).Encode(map[string]any{"projects": []Project{}}); err != nil {
			t.Fatalf("failed to write response: %v", err)
		}
	}))
	defer slowServer.Close()

	client := &commitmentsClient{
		keystone: &gophercloud.ServiceClient{
			ProviderClient: &gophercloud.ProviderClient{HTTPClient: *http.DefaultClient},
			Endpoint:       slowServer.URL + "/v3",
		},
	}

	// Create a context that will be cancelled immediately
	ctx, cancel := context.WithTimeout(context.Background(), 1*time.Millisecond)
	defer cancel()

	// This should fail due to context timeout
	projects, err := client.ListProjects(ctx)
	if err == nil {
		t.Fatal("expected error due to context cancellation, got nil")
	}
	if projects != nil {
		t.Errorf("expected nil projects, got %+v", projects)
	}
}
