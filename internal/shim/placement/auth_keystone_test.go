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
)

func TestKeystoneIntrospectorSuccess(t *testing.T) {
	expiry := time.Now().Add(time.Hour).UTC().Truncate(time.Second)

	ks := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v3/auth/tokens" {
			t.Errorf("unexpected path %q", r.URL.Path)
		}
		if r.Header.Get("X-Auth-Token") != "my-token" {
			t.Error("missing X-Auth-Token")
		}
		if r.Header.Get("X-Subject-Token") != "my-token" {
			t.Error("missing X-Subject-Token")
		}
		w.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(w).Encode(keystoneTokenResponse{
			Token: struct {
				ExpiresAt string `json:"expires_at"`
				Roles     []struct {
					Name string `json:"name"`
				} `json:"roles"`
				Project *struct {
					ID string `json:"id"`
				} `json:"project"`
			}{
				ExpiresAt: expiry.Format(time.RFC3339),
				Roles: []struct {
					Name string `json:"name"`
				}{
					{Name: "cloud_compute_admin"},
					{Name: "cloud_compute_viewer"},
				},
				Project: &struct {
					ID string `json:"id"`
				}{ID: "proj-abc"},
			},
		}); err != nil {
			t.Fatal(err)
		}
	}))
	defer ks.Close()

	ki := &keystoneIntrospector{keystoneURL: ks.URL, httpClient: ks.Client()}
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

	ki := &keystoneIntrospector{keystoneURL: ks.URL, httpClient: ks.Client()}
	_, err := ki.introspect(context.Background(), "bad-token")
	if err == nil {
		t.Fatal("expected error for invalid token")
	}
}

func TestKeystoneIntrospectorKeystoneDown(t *testing.T) {
	ki := &keystoneIntrospector{
		keystoneURL: "http://127.0.0.1:1",
		httpClient:  &http.Client{Timeout: 100 * time.Millisecond},
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
		if err := json.NewEncoder(w).Encode(keystoneTokenResponse{
			Token: struct {
				ExpiresAt string `json:"expires_at"`
				Roles     []struct {
					Name string `json:"name"`
				} `json:"roles"`
				Project *struct {
					ID string `json:"id"`
				} `json:"project"`
			}{
				ExpiresAt: expiry.Format(time.RFC3339),
				Roles: []struct {
					Name string `json:"name"`
				}{
					{Name: "admin"},
				},
				Project: nil, // domain-scoped, no project
			},
		}); err != nil {
			t.Fatal(err)
		}
	}))
	defer ks.Close()

	ki := &keystoneIntrospector{keystoneURL: ks.URL, httpClient: ks.Client()}
	info, err := ki.introspect(context.Background(), "domain-token")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if info.projectID != "" {
		t.Errorf("projectID = %q, want empty for domain-scoped token", info.projectID)
	}
}
