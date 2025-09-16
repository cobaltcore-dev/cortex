// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package commitments

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/cobaltcore-dev/cortex/internal/conf"
	"github.com/gophercloud/gophercloud/v2"
	"github.com/gophercloud/gophercloud/v2/openstack/identity/v3/projects"
)

// Test that NewCommitmentsClient returns a proper client instance
func TestNewCommitmentsClient(t *testing.T) {
	config := conf.KeystoneConfig{
		URL:                 "http://test.example.com",
		OSUsername:          "testuser",
		OSPassword:          "testpass",
		OSProjectName:       "testproject",
		OSUserDomainName:    "testdomain",
		OSProjectDomainName: "testprojectdomain",
	}

	client := NewCommitmentsClient(config)
	if client == nil {
		t.Fatal("NewCommitmentsClient returned nil")
	}

	// Verify internal configuration
	c, ok := client.(*commitmentsClient)
	if !ok {
		t.Fatal("NewCommitmentsClient did not return a *commitmentsClient")
	}

	if c.conf.URL != config.URL {
		t.Errorf("Expected URL %s, got %s", config.URL, c.conf.URL)
	}
	if c.conf.OSUsername != config.OSUsername {
		t.Errorf("Expected OSUsername %s, got %s", config.OSUsername, c.conf.OSUsername)
	}
}

// Test data structures and helper functions for tests
func createMockServiceClient(endpoint string) *gophercloud.ServiceClient {
	// Ensure endpoint has trailing slash for proper URL construction
	if !strings.HasSuffix(endpoint, "/") {
		endpoint += "/"
	}
	return &gophercloud.ServiceClient{
		ProviderClient: &gophercloud.ProviderClient{
			HTTPClient: *http.DefaultClient,
		},
		Endpoint: endpoint,
	}
}

func createTestFlavors() []Flavor {
	return []Flavor{
		{
			ID:          "flavor-1",
			Name:        "small",
			RAM:         1024,
			VCPUs:       1,
			Disk:        10,
			IsPublic:    true,
			RxTxFactor:  1.0,
			Ephemeral:   0,
			Description: "Small flavor",
			ExtraSpecs:  map[string]string{"hw:cpu_policy": "shared"},
		},
		{
			ID:          "flavor-2",
			Name:        "medium",
			RAM:         2048,
			VCPUs:       2,
			Disk:        20,
			IsPublic:    true,
			RxTxFactor:  1.0,
			Ephemeral:   5,
			Description: "Medium flavor",
			ExtraSpecs:  map[string]string{"hw:cpu_policy": "dedicated"},
		},
	}
}

func createTestProjects() []projects.Project {
	return []projects.Project{
		{
			ID:       "project-1",
			Name:     "test-project-1",
			DomainID: "domain-1",
			Enabled:  true,
		},
		{
			ID:       "project-2",
			Name:     "test-project-2",
			DomainID: "domain-1",
			Enabled:  true,
		},
	}
}

func createTestCommitments() []Commitment {
	now := uint64(time.Now().Unix())
	return []Commitment{
		{
			ID:               1,
			UUID:             "commitment-1-uuid",
			ServiceType:      "compute",
			ResourceName:     "instances_small",
			AvailabilityZone: "nova",
			Amount:           5,
			Unit:             "instances",
			Duration:         "1 year",
			CreatedAt:        now - 86400,
			ExpiresAt:        now + 31536000,
			Status:           "confirmed",
			NotifyOnConfirm:  false,
			ProjectID:        "project-1",
			DomainID:         "domain-1",
		},
		{
			ID:               2,
			UUID:             "commitment-2-uuid",
			ServiceType:      "compute",
			ResourceName:     "instances_medium",
			AvailabilityZone: "nova",
			Amount:           3,
			Unit:             "instances",
			Duration:         "6 months",
			CreatedAt:        now - 43200,
			ExpiresAt:        now + 15768000,
			Status:           "pending",
			NotifyOnConfirm:  true,
			ProjectID:        "project-2",
			DomainID:         "domain-1",
		},
		{
			ID:               3,
			UUID:             "commitment-3-uuid",
			ServiceType:      "network",
			ResourceName:     "networks",
			AvailabilityZone: "nova",
			Amount:           10,
			Unit:             "networks",
			Duration:         "1 month",
			CreatedAt:        now - 21600,
			ExpiresAt:        now + 2592000,
			Status:           "confirmed",
			NotifyOnConfirm:  false,
			ProjectID:        "project-1",
			DomainID:         "domain-1",
		},
	}
}

// Mock HTTP server for testing getAllFlavors
func TestCommitmentsClient_getAllFlavors(t *testing.T) {
	testFlavors := createTestFlavors()

	// Create mock server
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.Contains(r.URL.Path, "/flavors/detail") {
			t.Errorf("Expected request to /flavors/detail, got %s", r.URL.Path)
		}

		w.Header().Set("Content-Type", "application/json")
		response := struct {
			Flavors []Flavor `json:"flavors"`
		}{
			Flavors: testFlavors,
		}
		json.NewEncoder(w).Encode(response)
	}))
	defer server.Close()

	// Create client with mock service client
	client := &commitmentsClient{
		nova: createMockServiceClient(server.URL),
	}

	ctx := context.Background()
	flavors, err := client.getAllFlavors(ctx)

	if err != nil {
		t.Fatalf("getAllFlavors returned error: %v", err)
	}

	if len(flavors) != len(testFlavors) {
		t.Errorf("Expected %d flavors, got %d", len(testFlavors), len(flavors))
	}

	for i, flavor := range flavors {
		expectedFlavor := testFlavors[i]
		if flavor.ID != expectedFlavor.ID {
			t.Errorf("Expected flavor ID %s, got %s", expectedFlavor.ID, flavor.ID)
		}
		if flavor.Name != expectedFlavor.Name {
			t.Errorf("Expected flavor name %s, got %s", expectedFlavor.Name, flavor.Name)
		}
		if flavor.RAM != expectedFlavor.RAM {
			t.Errorf("Expected flavor RAM %d, got %d", expectedFlavor.RAM, flavor.RAM)
		}
		if flavor.VCPUs != expectedFlavor.VCPUs {
			t.Errorf("Expected flavor VCPUs %d, got %d", expectedFlavor.VCPUs, flavor.VCPUs)
		}
	}
}

// Test getAllFlavors error handling
func TestCommitmentsClient_getAllFlavors_Error(t *testing.T) {
	// Create mock server that returns error
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		w.Write([]byte("Internal Server Error"))
	}))
	defer server.Close()

	client := &commitmentsClient{
		nova: createMockServiceClient(server.URL),
	}

	ctx := context.Background()
	_, err := client.getAllFlavors(ctx)

	if err == nil {
		t.Fatal("Expected getAllFlavors to return error, got nil")
	}
}

// Test getAllProjects
func TestCommitmentsClient_getAllProjects(t *testing.T) {
	testProjects := createTestProjects()

	// Create mock server
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.Contains(r.URL.Path, "/projects") {
			t.Errorf("Expected request to /projects, got %s", r.URL.Path)
		}

		w.Header().Set("Content-Type", "application/json")
		response := struct {
			Projects []projects.Project `json:"projects"`
		}{
			Projects: testProjects,
		}
		json.NewEncoder(w).Encode(response)
	}))
	defer server.Close()

	client := &commitmentsClient{
		keystone: createMockServiceClient(server.URL),
	}

	ctx := context.Background()
	projects, err := client.getAllProjects(ctx)

	if err != nil {
		t.Fatalf("getAllProjects returned error: %v", err)
	}

	if len(projects) != len(testProjects) {
		t.Errorf("Expected %d projects, got %d", len(testProjects), len(projects))
	}

	for i, project := range projects {
		expectedProject := testProjects[i]
		if project.ID != expectedProject.ID {
			t.Errorf("Expected project ID %s, got %s", expectedProject.ID, project.ID)
		}
		if project.Name != expectedProject.Name {
			t.Errorf("Expected project name %s, got %s", expectedProject.Name, project.Name)
		}
		if project.DomainID != expectedProject.DomainID {
			t.Errorf("Expected project domain ID %s, got %s", expectedProject.DomainID, project.DomainID)
		}
	}
}

// Test getAllProjects error handling
func TestCommitmentsClient_getAllProjects_Error(t *testing.T) {
	// Create mock server that returns error
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		w.Write([]byte("Unauthorized"))
	}))
	defer server.Close()

	client := &commitmentsClient{
		keystone: createMockServiceClient(server.URL),
	}

	ctx := context.Background()
	_, err := client.getAllProjects(ctx)

	if err == nil {
		t.Fatal("Expected getAllProjects to return error, got nil")
	}
}

// Test getCommitments (private method)
func TestCommitmentsClient_getCommitments(t *testing.T) {
	testCommitments := createTestCommitments()
	project := createTestProjects()[0]

	// Create mock server
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		expectedPath := fmt.Sprintf("/v1/domains/%s/projects/%s/commitments", project.DomainID, project.ID)
		if !strings.Contains(r.URL.Path, expectedPath) {
			t.Errorf("Expected request to %s, got %s", expectedPath, r.URL.Path)
		}

		// Check auth token header
		if r.Header.Get("X-Auth-Token") == "" {
			t.Error("Expected X-Auth-Token header to be set")
		}

		w.Header().Set("Content-Type", "application/json")
		response := struct {
			Commitments []Commitment `json:"commitments"`
		}{
			Commitments: testCommitments,
		}
		json.NewEncoder(w).Encode(response)
	}))
	defer server.Close()

	// Create mock service client with token
	serviceClient := createMockServiceClient(server.URL)
	serviceClient.SetToken("test-token")

	client := &commitmentsClient{
		limes: serviceClient,
	}

	ctx := context.Background()
	commitments, err := client.getCommitments(ctx, project)

	if err != nil {
		t.Fatalf("getCommitments returned error: %v", err)
	}

	// Should only return compute commitments
	expectedComputeCommitments := 0
	for _, c := range testCommitments {
		if c.ServiceType == "compute" {
			expectedComputeCommitments++
		}
	}

	if len(commitments) != expectedComputeCommitments {
		t.Errorf("Expected %d compute commitments, got %d", expectedComputeCommitments, len(commitments))
	}

	// Verify all returned commitments are compute type and have project info
	for _, commitment := range commitments {
		if commitment.ServiceType != "compute" {
			t.Errorf("Expected compute commitment, got %s", commitment.ServiceType)
		}
		if commitment.ProjectID != project.ID {
			t.Errorf("Expected project ID %s, got %s", project.ID, commitment.ProjectID)
		}
		if commitment.DomainID != project.DomainID {
			t.Errorf("Expected domain ID %s, got %s", project.DomainID, commitment.DomainID)
		}
	}
}

// Test getCommitments error cases
func TestCommitmentsClient_getCommitments_ErrorCases(t *testing.T) {
	project := createTestProjects()[0]

	testCases := []struct {
		name        string
		statusCode  int
		response    string
		expectError bool
	}{
		{
			name:        "HTTP 404 Not Found",
			statusCode:  http.StatusNotFound,
			response:    "Not Found",
			expectError: true,
		},
		{
			name:        "HTTP 500 Internal Server Error",
			statusCode:  http.StatusInternalServerError,
			response:    "Internal Server Error",
			expectError: true,
		},
		{
			name:        "Invalid JSON response",
			statusCode:  http.StatusOK,
			response:    "invalid json",
			expectError: true,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(tc.statusCode)
				w.Write([]byte(tc.response))
			}))
			defer server.Close()

			serviceClient := createMockServiceClient(server.URL)
			serviceClient.SetToken("test-token")

			client := &commitmentsClient{
				limes: serviceClient,
			}

			ctx := context.Background()
			_, err := client.getCommitments(ctx, project)

			if tc.expectError && err == nil {
				t.Fatal("Expected error, got nil")
			}
			if !tc.expectError && err != nil {
				t.Fatalf("Expected no error, got %v", err)
			}
		})
	}
}

// Test GetComputeCommitments integration
func TestCommitmentsClient_GetComputeCommitments(t *testing.T) {
	testProjects := createTestProjects()
	testFlavors := createTestFlavors()
	testCommitments := createTestCommitments()

	// Create mock servers for different services
	keystoneServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		response := struct {
			Projects []projects.Project `json:"projects"`
		}{
			Projects: testProjects,
		}
		json.NewEncoder(w).Encode(response)
	}))
	defer keystoneServer.Close()

	novaServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		response := struct {
			Flavors []Flavor `json:"flavors"`
		}{
			Flavors: testFlavors,
		}
		json.NewEncoder(w).Encode(response)
	}))
	defer novaServer.Close()

	limesServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		response := struct {
			Commitments []Commitment `json:"commitments"`
		}{
			Commitments: testCommitments,
		}
		json.NewEncoder(w).Encode(response)
	}))
	defer limesServer.Close()

	// Create client with mock service clients
	keystoneClient := createMockServiceClient(keystoneServer.URL)
	novaClient := createMockServiceClient(novaServer.URL)
	limesClient := createMockServiceClient(limesServer.URL)
	limesClient.SetToken("test-token")

	client := &commitmentsClient{
		keystone: keystoneClient,
		nova:     novaClient,
		limes:    limesClient,
	}

	ctx := context.Background()
	commitments, err := client.GetComputeCommitments(ctx)

	if err != nil {
		t.Fatalf("GetComputeCommitments returned error: %v", err)
	}

	// Should return compute commitments for all projects
	expectedComputeCommitments := 0
	for _, c := range testCommitments {
		if c.ServiceType == "compute" {
			expectedComputeCommitments++
		}
	}
	expectedTotal := expectedComputeCommitments * len(testProjects)

	if len(commitments) != expectedTotal {
		t.Errorf("Expected %d commitments, got %d", expectedTotal, len(commitments))
	}

	// Verify flavor resolution for instance commitments
	flavorResolved := false
	for _, commitment := range commitments {
		if strings.HasPrefix(commitment.ResourceName, "instances_") {
			flavorName := strings.TrimPrefix(commitment.ResourceName, "instances_")
			if commitment.Flavor != nil && commitment.Flavor.Name == flavorName {
				flavorResolved = true
				break
			}
		}
	}

	if !flavorResolved {
		t.Error("Expected at least one instance commitment to have resolved flavor")
	}
}

// Test GetComputeCommitments error handling
func TestCommitmentsClient_GetComputeCommitments_ErrorHandling(t *testing.T) {
	testCases := []struct {
		name            string
		keystoneFailure bool
		novaFailure     bool
		limesFailure    bool
		expectError     bool
	}{
		{
			name:            "Keystone failure",
			keystoneFailure: true,
			expectError:     true,
		},
		{
			name:        "Nova failure",
			novaFailure: true,
			expectError: true,
		},
		{
			name:         "Limes failure",
			limesFailure: true,
			expectError:  true,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			// Create servers that fail or succeed based on test case
			keystoneServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if tc.keystoneFailure {
					w.WriteHeader(http.StatusInternalServerError)
					return
				}
				w.Header().Set("Content-Type", "application/json")
				response := struct {
					Projects []projects.Project `json:"projects"`
				}{
					Projects: createTestProjects(),
				}
				json.NewEncoder(w).Encode(response)
			}))
			defer keystoneServer.Close()

			novaServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if tc.novaFailure {
					w.WriteHeader(http.StatusInternalServerError)
					return
				}
				w.Header().Set("Content-Type", "application/json")
				response := struct {
					Flavors []Flavor `json:"flavors"`
				}{
					Flavors: createTestFlavors(),
				}
				json.NewEncoder(w).Encode(response)
			}))
			defer novaServer.Close()

			limesServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if tc.limesFailure {
					w.WriteHeader(http.StatusInternalServerError)
					return
				}
				w.Header().Set("Content-Type", "application/json")
				response := struct {
					Commitments []Commitment `json:"commitments"`
				}{
					Commitments: createTestCommitments(),
				}
				json.NewEncoder(w).Encode(response)
			}))
			defer limesServer.Close()

			keystoneClient := createMockServiceClient(keystoneServer.URL)
			novaClient := createMockServiceClient(novaServer.URL)
			limesClient := createMockServiceClient(limesServer.URL)
			limesClient.SetToken("test-token")

			client := &commitmentsClient{
				keystone: keystoneClient,
				nova:     novaClient,
				limes:    limesClient,
			}

			ctx := context.Background()
			_, err := client.GetComputeCommitments(ctx)

			if tc.expectError && err == nil {
				t.Fatal("Expected error, got nil")
			}
			if !tc.expectError && err != nil {
				t.Fatalf("Expected no error, got %v", err)
			}
		})
	}
}

// Test flavor resolution logic
func TestCommitmentsClient_FlavorResolution(t *testing.T) {
	testFlavors := createTestFlavors()

	// Create commitments with various resource names
	commitments := []Commitment{
		{
			ID:           1,
			ServiceType:  "compute",
			ResourceName: "instances_small", // Should resolve to small flavor
		},
		{
			ID:           2,
			ServiceType:  "compute",
			ResourceName: "instances_medium", // Should resolve to medium flavor
		},
		{
			ID:           3,
			ServiceType:  "compute",
			ResourceName: "instances_nonexistent", // Should not resolve
		},
		{
			ID:           4,
			ServiceType:  "compute",
			ResourceName: "cores", // Not an instance commitment
		},
	}

	// Create flavor map
	flavorsByName := make(map[string]Flavor, len(testFlavors))
	for _, flavor := range testFlavors {
		flavorsByName[flavor.Name] = flavor
	}

	// Apply flavor resolution logic
	for i := range commitments {
		if !strings.HasPrefix(commitments[i].ResourceName, "instances_") {
			continue
		}
		flavorName := strings.TrimPrefix(commitments[i].ResourceName, "instances_")
		if flavor, ok := flavorsByName[flavorName]; ok {
			commitments[i].Flavor = &flavor
		}
	}

	// Verify results
	if commitments[0].Flavor == nil || commitments[0].Flavor.Name != "small" {
		t.Error("Expected small flavor to be resolved for instances_small commitment")
	}

	if commitments[1].Flavor == nil || commitments[1].Flavor.Name != "medium" {
		t.Error("Expected medium flavor to be resolved for instances_medium commitment")
	}

	if commitments[2].Flavor != nil {
		t.Error("Expected no flavor to be resolved for instances_nonexistent commitment")
	}

	if commitments[3].Flavor != nil {
		t.Error("Expected no flavor to be resolved for cores commitment")
	}
}

// Test context cancellation
func TestCommitmentsClient_ContextCancellation(t *testing.T) {
	// Create a server that delays response to test cancellation
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(100 * time.Millisecond)
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(struct {
			Projects []projects.Project `json:"projects"`
		}{
			Projects: createTestProjects(),
		})
	}))
	defer server.Close()

	client := &commitmentsClient{
		keystone: createMockServiceClient(server.URL),
	}

	// Create context that will be cancelled
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // Cancel immediately

	_, err := client.getAllProjects(ctx)

	if err == nil {
		t.Fatal("Expected error due to context cancellation, got nil")
	}

	if !strings.Contains(err.Error(), "context canceled") {
		t.Errorf("Expected context cancellation error, got: %v", err)
	}
}
