// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package placement

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gophercloud/gophercloud/v2"
)

func TestKeystoneIntrospectorSuccess(t *testing.T) {
	expiry := time.Now().Add(time.Hour).UTC().Truncate(time.Second)

	ks := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/auth/tokens" {
			t.Errorf("unexpected path %q", r.URL.Path)
		}
		if r.Header.Get("X-Subject-Token") != "my-token" {
			t.Error("missing X-Subject-Token")
		}
		w.Header().Set("Content-Type", "application/json")
		resp := map[string]any{
			"token": map[string]any{
				"expires_at": expiry.Format(time.RFC3339),
				"roles": []map[string]any{
					{"name": "cloud_compute_admin"},
					{"name": "cloud_compute_viewer"},
				},
				"project": map[string]any{
					"id":   "proj-abc",
					"name": "my-project",
					"domain": map[string]any{
						"id":   "default",
						"name": "Default",
					},
				},
			},
		}
		if err := json.NewEncoder(w).Encode(resp); err != nil {
			t.Fatal(err)
		}
	}))
	defer ks.Close()

	ki := &keystoneIntrospector{
		identityClient: &gophercloud.ServiceClient{
			ProviderClient: &gophercloud.ProviderClient{},
			Endpoint:       ks.URL + "/",
		},
	}
	info, err := ki.introspect(context.Background(), "my-token")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(info.roles) != 2 {
		t.Fatalf("roles = %v, want 2 roles", info.roles)
	}
	if info.roles[0] != "cloud_compute_admin" || info.roles[1] != "cloud_compute_viewer" {
		t.Errorf("roles = %v", info.roles)
	}
	if info.projectID != "proj-abc" {
		t.Errorf("projectID = %q, want %q", info.projectID, "proj-abc")
	}
	if !info.expiresAt.Equal(expiry) {
		t.Errorf("expiresAt = %v, want %v", info.expiresAt, expiry)
	}
}

func TestKeystoneIntrospectorInvalidToken(t *testing.T) {
	ks := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer ks.Close()

	ki := &keystoneIntrospector{
		identityClient: &gophercloud.ServiceClient{
			ProviderClient: &gophercloud.ProviderClient{},
			Endpoint:       ks.URL + "/",
		},
	}
	_, err := ki.introspect(context.Background(), "bad-token")
	if err == nil {
		t.Fatal("expected error for invalid token")
	}
}

func TestKeystoneIntrospectorKeystoneDown(t *testing.T) {
	ki := &keystoneIntrospector{
		identityClient: &gophercloud.ServiceClient{
			ProviderClient: &gophercloud.ProviderClient{
				HTTPClient: http.Client{Timeout: 100 * time.Millisecond},
			},
			Endpoint: "http://127.0.0.1:1/",
		},
	}
	_, err := ki.introspect(context.Background(), "token")
	if err == nil {
		t.Fatal("expected error when keystone is unreachable")
	}
}

func TestKeystoneIntrospectorDomainScopedToken(t *testing.T) {
	expiry := time.Now().Add(time.Hour).UTC().Truncate(time.Second)

	ks := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		resp := map[string]any{
			"token": map[string]any{
				"expires_at": expiry.Format(time.RFC3339),
				"roles": []map[string]any{
					{"name": "admin"},
				},
			},
		}
		if err := json.NewEncoder(w).Encode(resp); err != nil {
			t.Fatal(err)
		}
	}))
	defer ks.Close()

	ki := &keystoneIntrospector{
		identityClient: &gophercloud.ServiceClient{
			ProviderClient: &gophercloud.ProviderClient{},
			Endpoint:       ks.URL + "/",
		},
	}
	info, err := ki.introspect(context.Background(), "domain-token")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if info.projectID != "" {
		t.Errorf("projectID = %q, want empty for domain-scoped token", info.projectID)
	}
}
