// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package openstack

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/cobaltcore-dev/cortex/internal/conf"
)

var exampleConfig = conf.SyncOpenStackConfig{
	KeystoneURL:         "http://auth.url",
	OSUsername:          "username",
	OSPassword:          "password",
	OSProjectName:       "project_name",
	OSUserDomainName:    "user_domain_name",
	OSProjectDomainName: "project_domain_name",
}

func TestGetKeystoneAuth(t *testing.T) {
	// Mock the OpenStack Identity service response
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/auth/tokens" && r.Method == http.MethodPost {
			w.Header().Set("X-Subject-Token", "test_token")
			w.WriteHeader(http.StatusCreated)
			//nolint:errcheck
			json.NewEncoder(w).Encode(struct{}{}) // Don't care about the content
		} else {
			w.WriteHeader(http.StatusNotFound)
		}
	})

	server := httptest.NewServer(handler)
	defer server.Close()

	// Override the OS_AUTH_URL to point to the mock server
	exampleConfig.KeystoneURL = server.URL
	keystoneAPI := &keystoneAPI{
		conf: exampleConfig,
	}
	auth, err := keystoneAPI.Authenticate()
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	// Verify the results
	if auth.token != "test_token" {
		t.Errorf("expected token to be %s, got %s", "test_token", auth.token)
	}
}

func TestGetKeystoneAuthFailure(t *testing.T) {
	// Mock the OpenStack Identity service response
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	})

	server := httptest.NewServer(handler)
	defer server.Close()

	// Override the OS_AUTH_URL to point to the mock server
	exampleConfig.KeystoneURL = server.URL
	keystoneAPI := &keystoneAPI{
		conf: exampleConfig,
	}
	_, err := keystoneAPI.Authenticate()
	if err == nil {
		t.Fatalf("expected error, got none")
	}
}
