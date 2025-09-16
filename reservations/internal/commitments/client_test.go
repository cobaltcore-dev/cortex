// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package commitments

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/cobaltcore-dev/cortex/internal/conf"
	"github.com/gophercloud/gophercloud/v2"
)

func TestNewCommitmentsClient(t *testing.T) {
	config := conf.KeystoneConfig{
		URL:                 "http://keystone.example.com",
		OSUsername:          "testuser",
		OSPassword:          "testpass",
		OSProjectName:       "testproject",
		OSUserDomainName:    "default",
		OSProjectDomainName: "default",
	}

	client := NewCommitmentsClient(config)
	if client == nil {
		t.Fatal("expected client to be created, got nil")
	}

	// Check that the returned client is of the correct type
	concreteClient, ok := client.(*commitmentsClient)
	if !ok {
		t.Fatal("expected client to be of type *commitmentsClient")
	}

	// Verify config is set correctly
	if concreteClient.conf.URL != config.URL {
		t.Errorf("expected URL %s, got %s", config.URL, concreteClient.conf.URL)
	}
	if concreteClient.conf.OSUsername != config.OSUsername {
		t.Errorf("expected username %s, got %s", config.OSUsername, concreteClient.conf.OSUsername)
	}
}

func TestCommitmentsClient_ListProjects(t *testing.T) {
	// Mock server for Keystone identity service
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v3/projects" {
			// Return raw JSON string as the gophercloud pages expect
			w.Header().Set("Content-Type", "application/json")
			w.Write([]byte(`{
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

func TestCommitmentsClient_ListFlavorsByName(t *testing.T) {
	// Mock server for Nova compute service
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "/flavors/detail") {
			// Return raw JSON string as the gophercloud pages expect
			w.Header().Set("Content-Type", "application/json")
			w.Write([]byte(`{
				"flavors": [
					{
						"id": "flavor1",
						"name": "m1.small",
						"ram": 2048,
						"vcpus": 1,
						"disk": 20,
						"rxtx_factor": 1.0,
						"os-flavor-access:is_public": true,
						"OS-FLV-EXT-DATA:ephemeral": 0,
						"description": "Small flavor",
						"extra_specs": {"hw:cpu_policy": "shared"}
					},
					{
						"id": "flavor2",
						"name": "m1.medium",
						"ram": 4096,
						"vcpus": 2,
						"disk": 40,
						"rxtx_factor": 1.0,
						"os-flavor-access:is_public": true,
						"OS-FLV-EXT-DATA:ephemeral": 0,
						"description": "Medium flavor",
						"extra_specs": {"hw:cpu_policy": "dedicated"}
					}
				]
			}`))
			return
		}
		http.NotFound(w, r)
	}))
	defer server.Close()

	client := &commitmentsClient{
		nova: &gophercloud.ServiceClient{
			ProviderClient: &gophercloud.ProviderClient{
				HTTPClient: *http.DefaultClient,
			},
			Endpoint:     server.URL + "/",
			Microversion: "2.61",
		},
	}

	ctx := context.Background()
	flavorsByName, err := client.ListFlavorsByName(ctx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	expectedFlavors := map[string]Flavor{
		"m1.small": {
			ID:          "flavor1",
			Name:        "m1.small",
			RAM:         2048,
			VCPUs:       1,
			Disk:        20,
			RxTxFactor:  1.0,
			IsPublic:    true,
			Ephemeral:   0,
			Description: "Small flavor",
			ExtraSpecs:  map[string]string{"hw:cpu_policy": "shared"},
		},
		"m1.medium": {
			ID:          "flavor2",
			Name:        "m1.medium",
			RAM:         4096,
			VCPUs:       2,
			Disk:        40,
			RxTxFactor:  1.0,
			IsPublic:    true,
			Ephemeral:   0,
			Description: "Medium flavor",
			ExtraSpecs:  map[string]string{"hw:cpu_policy": "dedicated"},
		},
	}

	if len(flavorsByName) != len(expectedFlavors) {
		t.Fatalf("expected %d flavors, got %d", len(expectedFlavors), len(flavorsByName))
	}

	for name, expected := range expectedFlavors {
		actual, exists := flavorsByName[name]
		if !exists {
			t.Errorf("expected flavor %s to exist", name)
			continue
		}
		if !reflect.DeepEqual(actual, expected) {
			t.Errorf("flavor %s: expected %+v, got %+v", name, expected, actual)
		}
	}
}

func TestCommitmentsClient_ListFlavorsByName_Error(t *testing.T) {
	// Mock server that returns an error
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "Service Unavailable", http.StatusServiceUnavailable)
	}))
	defer server.Close()

	client := &commitmentsClient{
		nova: &gophercloud.ServiceClient{
			ProviderClient: &gophercloud.ProviderClient{
				HTTPClient: *http.DefaultClient,
			},
			Endpoint: server.URL + "/",
		},
	}

	ctx := context.Background()
	flavors, err := client.ListFlavorsByName(ctx)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if flavors != nil {
		t.Errorf("expected nil flavors, got %+v", flavors)
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
						CreatedAt:        uint64(time.Now().Unix()),
						ExpiresAt:        uint64(time.Now().Add(365 * 24 * time.Hour).Unix()),
						Status:           "confirmed",
						ProjectID:        projectID,
						DomainID:         domainID,
					},
				},
			}
			json.NewEncoder(w).Encode(response)
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

func TestCommitmentsClient_ListCommitmentsByID_Error(t *testing.T) {
	// Mock server that returns an error
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
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
		{ID: "project1", DomainID: "domain1"},
	}

	ctx := context.Background()
	commitments, err := client.ListCommitmentsByID(ctx, projects...)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if commitments != nil {
		t.Errorf("expected nil commitments, got %+v", commitments)
	}
}

func TestCommitmentsClient_ListActiveServersByProjectID(t *testing.T) {
	// Mock server for Nova compute service
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "/servers/detail") {
			// Parse query parameters to determine which project
			tenantID := r.URL.Query().Get("tenant_id")
			status := r.URL.Query().Get("status")

			if status != "ACTIVE" {
				t.Errorf("expected status=ACTIVE, got %s", status)
			}

			// Return raw JSON string as the gophercloud pages expect
			w.Header().Set("Content-Type", "application/json")
			if tenantID == "project1" {
				w.Write([]byte(`{
					"servers": [
						{
							"id": "server1",
							"name": "test-server-1",
							"status": "ACTIVE",
							"tenant_id": "project1",
							"flavor": {"original_name": "m1.small"}
						}
					]
				}`))
			} else {
				w.Write([]byte(`{"servers": []}`))
			}
			return
		}
		http.NotFound(w, r)
	}))
	defer server.Close()

	client := &commitmentsClient{
		nova: &gophercloud.ServiceClient{
			ProviderClient: &gophercloud.ProviderClient{
				HTTPClient: *http.DefaultClient,
			},
			Endpoint: server.URL + "/",
		},
	}

	projects := []Project{
		{ID: "project1", Name: "Test Project 1"},
		{ID: "project2", Name: "Test Project 2"},
	}

	ctx := context.Background()
	serversByProject, err := client.ListActiveServersByProjectID(ctx, projects...)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(serversByProject) != 2 {
		t.Fatalf("expected 2 project entries, got %d", len(serversByProject))
	}

	// Check project1 has 1 server
	servers1, exists := serversByProject["project1"]
	if !exists {
		t.Fatal("expected project1 to exist in results")
	}
	if len(servers1) != 1 {
		t.Fatalf("expected 1 server for project1, got %d", len(servers1))
	}
	if servers1[0].ID != "server1" {
		t.Errorf("expected server ID server1, got %s", servers1[0].ID)
	}

	// Check project2 has 0 servers
	servers2, exists := serversByProject["project2"]
	if !exists {
		t.Fatal("expected project2 to exist in results")
	}
	if len(servers2) != 0 {
		t.Fatalf("expected 0 servers for project2, got %d", len(servers2))
	}
}

func TestCommitmentsClient_ListActiveServersByProjectID_Error(t *testing.T) {
	// Mock server that returns an error
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "Forbidden", http.StatusForbidden)
	}))
	defer server.Close()

	client := &commitmentsClient{
		nova: &gophercloud.ServiceClient{
			ProviderClient: &gophercloud.ProviderClient{
				HTTPClient: *http.DefaultClient,
			},
			Endpoint: server.URL + "/",
		},
	}

	projects := []Project{
		{ID: "project1"},
	}

	ctx := context.Background()
	servers, err := client.ListActiveServersByProjectID(ctx, projects...)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if servers != nil {
		t.Errorf("expected nil servers, got %+v", servers)
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
		json.NewEncoder(w).Encode(response)
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

	expectedError := "unexpected status code: 404"
	if err.Error() != expectedError {
		t.Errorf("expected error %q, got %q", expectedError, err.Error())
	}
}

func TestCommitmentsClient_listCommitments_JSONError(t *testing.T) {
	// Mock server that returns invalid JSON
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("invalid json"))
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

func TestCommitmentsClient_listActiveServersForProject(t *testing.T) {
	// Mock server for Nova compute service
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.Contains(r.URL.Path, "/servers/detail") {
			http.NotFound(w, r)
			return
		}

		// Verify query parameters
		query := r.URL.Query()
		if query.Get("all_tenants") != "true" {
			t.Errorf("expected all_tenants=true, got %s", query.Get("all_tenants"))
		}
		if query.Get("tenant_id") != "test-project" {
			t.Errorf("expected tenant_id=test-project, got %s", query.Get("tenant_id"))
		}
		if query.Get("status") != "ACTIVE" {
			t.Errorf("expected status=ACTIVE, got %s", query.Get("status"))
		}

		// Return raw JSON string as the gophercloud pages expect
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{
			"servers": [
				{
					"id": "server1",
					"name": "test-server",
					"status": "ACTIVE",
					"tenant_id": "test-project",
					"flavor": {"original_name": "m1.small"}
				},
				{
					"id": "server2",
					"name": "another-server",
					"status": "ACTIVE",
					"tenant_id": "test-project",
					"flavor": {"original_name": "m1.medium"}
				}
			]
		}`))
	}))
	defer server.Close()

	client := &commitmentsClient{
		nova: &gophercloud.ServiceClient{
			ProviderClient: &gophercloud.ProviderClient{
				HTTPClient: *http.DefaultClient,
			},
			Endpoint: server.URL + "/",
		},
	}

	project := Project{
		ID:   "test-project",
		Name: "Test Project",
	}

	ctx := context.Background()
	servers, err := client.listActiveServersForProject(ctx, project)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(servers) != 2 {
		t.Fatalf("expected 2 servers, got %d", len(servers))
	}

	expectedServers := []Server{
		{
			ID:         "server1",
			Name:       "test-server",
			Status:     "ACTIVE",
			TenantID:   "test-project",
			FlavorName: "m1.small",
		},
		{
			ID:         "server2",
			Name:       "another-server",
			Status:     "ACTIVE",
			TenantID:   "test-project",
			FlavorName: "m1.medium",
		},
	}

	for i, expected := range expectedServers {
		if servers[i].ID != expected.ID {
			t.Errorf("server %d: expected ID %s, got %s", i, expected.ID, servers[i].ID)
		}
		if servers[i].Name != expected.Name {
			t.Errorf("server %d: expected Name %s, got %s", i, expected.Name, servers[i].Name)
		}
		if servers[i].Status != expected.Status {
			t.Errorf("server %d: expected Status %s, got %s", i, expected.Status, servers[i].Status)
		}
		if servers[i].TenantID != expected.TenantID {
			t.Errorf("server %d: expected TenantID %s, got %s", i, expected.TenantID, servers[i].TenantID)
		}
		if servers[i].FlavorName != expected.FlavorName {
			t.Errorf("server %d: expected FlavorName %s, got %s", i, expected.FlavorName, servers[i].FlavorName)
		}
	}
}

func TestCommitmentsClient_listActiveServersForProject_Error(t *testing.T) {
	// Mock server that returns an error
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
	}))
	defer server.Close()

	client := &commitmentsClient{
		nova: &gophercloud.ServiceClient{
			ProviderClient: &gophercloud.ProviderClient{
				HTTPClient: *http.DefaultClient,
			},
			Endpoint: server.URL,
		},
	}

	project := Project{ID: "test-project"}

	ctx := context.Background()
	servers, err := client.listActiveServersForProject(ctx, project)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if servers != nil {
		t.Errorf("expected nil servers, got %+v", servers)
	}
}

func TestCommitmentsClient_ContextCancellation(t *testing.T) {
	// Test context cancellation handling
	slowServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Simulate a slow server
		time.Sleep(100 * time.Millisecond)
		json.NewEncoder(w).Encode(map[string]any{"projects": []Project{}})
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
