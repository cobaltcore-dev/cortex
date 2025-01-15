package openstack

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestGetKeystoneAuth(t *testing.T) {
	// Set up environment variables
	t.Setenv("OS_AUTH_URL", "http://auth.url")
	t.Setenv("OS_USERNAME", "username")
	t.Setenv("OS_PASSWORD", "password")
	t.Setenv("OS_PROJECT_NAME", "project_name")
	t.Setenv("OS_USER_DOMAIN_NAME", "user_domain_name")
	t.Setenv("OS_PROJECT_DOMAIN_NAME", "project_domain_name")

	// Mock the OpenStack Identity service response
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/auth/tokens" && r.Method == http.MethodPost {
			w.Header().Set("X-Subject-Token", "test_token")
			w.WriteHeader(http.StatusCreated)
			//nolint:errcheck
			json.NewEncoder(w).Encode(openStackAuthResponse{
				TokenMetadata: openStackAuthTokenMetadata{
					Catalog: []openStackService{
						{
							Name: "nova",
							Type: "compute",
							Endpoints: []openStackEndpoint{
								{URL: "http://nova.url"},
							},
						},
					},
				},
			})
		} else {
			w.WriteHeader(http.StatusNotFound)
		}
	})

	server := httptest.NewServer(handler)
	defer server.Close()

	// Override the OS_AUTH_URL to point to the mock server
	t.Setenv("OS_AUTH_URL", server.URL)

	// Call the function to test
	auth, err := getKeystoneAuth()
	if err != nil {
		t.Fatalf("Expected no error, got %v", err)
	}

	// Verify the results
	if auth.token != "test_token" {
		t.Errorf("Expected token to be %s, got %s", "test_token", auth.token)
	}
	if auth.nova.URL != "http://nova.url" {
		t.Errorf("Expected Nova URL to be %s, got %s", "http://nova.url", auth.nova.URL)
	}
}

func TestGetKeystoneAuthFailure(t *testing.T) {
	// Set up environment variables
	t.Setenv("OS_AUTH_URL", "http://auth.url")
	t.Setenv("OS_USERNAME", "username")
	t.Setenv("OS_PASSWORD", "password")
	t.Setenv("OS_PROJECT_NAME", "project_name")
	t.Setenv("OS_USER_DOMAIN_NAME", "user_domain_name")
	t.Setenv("OS_PROJECT_DOMAIN_NAME", "project_domain_name")

	// Mock the OpenStack Identity service response
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	})

	server := httptest.NewServer(handler)
	defer server.Close()

	// Override the OS_AUTH_URL to point to the mock server
	t.Setenv("OS_AUTH_URL", server.URL)

	// Call the function to test
	_, err := getKeystoneAuth()
	if err == nil {
		t.Fatalf("Expected error, got none")
	}
}
