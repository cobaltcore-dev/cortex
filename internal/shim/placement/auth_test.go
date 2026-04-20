// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package placement

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

// mockIntrospector is a test double for tokenIntrospector.
type mockIntrospector struct {
	info  *tokenInfo
	err   error
	calls atomic.Int64
}

func (m *mockIntrospector) introspect(_ context.Context, _ string) (*tokenInfo, error) {
	m.calls.Add(1)
	return m.info, m.err
}

func TestMatchPath(t *testing.T) {
	tests := []struct {
		pattern string
		path    string
		want    bool
	}{
		{"/usages", "/usages", true},
		{"/usages", "/traits", false},
		{"/resource_providers/*", "/resource_providers/abc", true},
		{"/resource_providers/*", "/resource_providers/abc/inventories", true},
		{"/resource_providers/*", "/resource_providers", true},
		{"/*", "/anything", true},
		{"/*", "/", true},
		{"*", "/anything", true},
		{"/", "/", true},
		{"/", "/other", false},
	}
	for _, tt := range tests {
		t.Run(tt.pattern+" vs "+tt.path, func(t *testing.T) {
			if got := matchPath(tt.pattern, tt.path); got != tt.want {
				t.Errorf("matchPath(%q, %q) = %v, want %v", tt.pattern, tt.path, got, tt.want)
			}
		})
	}
}

func TestMatchPolicy(t *testing.T) {
	tests := []struct {
		name   string
		policy compiledPolicy
		method string
		path   string
		want   bool
	}{
		{
			name:   "wildcard method matches GET",
			policy: compiledPolicy{method: "*", pathPattern: "/usages"},
			method: "GET", path: "/usages", want: true,
		},
		{
			name:   "wildcard method matches POST",
			policy: compiledPolicy{method: "*", pathPattern: "/usages"},
			method: "POST", path: "/usages", want: true,
		},
		{
			name:   "specific method matches",
			policy: compiledPolicy{method: "GET", pathPattern: "/usages"},
			method: "GET", path: "/usages", want: true,
		},
		{
			name:   "specific method does not match",
			policy: compiledPolicy{method: "GET", pathPattern: "/usages"},
			method: "POST", path: "/usages", want: false,
		},
		{
			name:   "catch-all matches everything",
			policy: compiledPolicy{method: "*", pathPattern: "/*"},
			method: "DELETE", path: "/anything/here", want: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := matchPolicy(&tt.policy, tt.method, tt.path); got != tt.want {
				t.Errorf("matchPolicy() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestTokenCache(t *testing.T) {
	t.Run("put and get", func(t *testing.T) {
		c := &tokenCache{ttl: time.Minute}
		info := &tokenInfo{
			roles:     []string{"admin"},
			projectID: "p1",
			expiresAt: time.Now().Add(time.Hour),
			cachedAt:  time.Now(),
		}
		c.put("tok", info)
		got, ok := c.get("tok")
		if !ok {
			t.Fatal("expected cache hit")
		}
		if got.projectID != "p1" {
			t.Errorf("projectID = %q, want %q", got.projectID, "p1")
		}
	})

	t.Run("miss for unknown token", func(t *testing.T) {
		c := &tokenCache{ttl: time.Minute}
		if _, ok := c.get("unknown"); ok {
			t.Fatal("expected cache miss")
		}
	})

	t.Run("expired TTL returns miss", func(t *testing.T) {
		c := &tokenCache{ttl: time.Millisecond}
		info := &tokenInfo{
			expiresAt: time.Now().Add(time.Hour),
			cachedAt:  time.Now().Add(-time.Second),
		}
		c.put("tok", info)
		if _, ok := c.get("tok"); ok {
			t.Fatal("expected cache miss due to TTL")
		}
	})

	t.Run("expired token returns miss", func(t *testing.T) {
		c := &tokenCache{ttl: time.Hour}
		info := &tokenInfo{
			expiresAt: time.Now().Add(-time.Second),
			cachedAt:  time.Now(),
		}
		c.put("tok", info)
		if _, ok := c.get("tok"); ok {
			t.Fatal("expected cache miss due to token expiry")
		}
	})
}

func TestCheckAuthDisabled(t *testing.T) {
	s := &Shim{} // authPolicies is nil
	req := httptest.NewRequest(http.MethodGet, "/anything", http.NoBody)
	w := httptest.NewRecorder()
	if !s.checkAuth(w, req) {
		t.Fatal("expected passthrough when auth disabled")
	}
}

func TestCheckAuthNoMatchingPolicy(t *testing.T) {
	s := &Shim{
		authPolicies: []compiledPolicy{
			{method: "GET", pathPattern: "/usages", roles: []authPolicyRole{{Name: "admin"}}},
		},
		tokenCache:        &tokenCache{ttl: time.Minute},
		tokenIntrospector: &mockIntrospector{},
	}
	req := httptest.NewRequest(http.MethodGet, "/no-such-path", http.NoBody)
	w := httptest.NewRecorder()
	if s.checkAuth(w, req) {
		t.Fatal("expected deny for unmatched path")
	}
	if w.Code != http.StatusForbidden {
		t.Errorf("status = %d, want %d", w.Code, http.StatusForbidden)
	}
}

func TestCheckAuthPublicEndpoint(t *testing.T) {
	t.Run("nil roles", func(t *testing.T) {
		s := &Shim{
			authPolicies: []compiledPolicy{
				{method: "GET", pathPattern: "/", roles: nil},
			},
			tokenCache:        &tokenCache{ttl: time.Minute},
			tokenIntrospector: &mockIntrospector{},
		}
		req := httptest.NewRequest(http.MethodGet, "/", http.NoBody)
		w := httptest.NewRecorder()
		if !s.checkAuth(w, req) {
			t.Fatal("expected passthrough for public endpoint (nil roles)")
		}
	})
	t.Run("empty roles", func(t *testing.T) {
		s := &Shim{
			authPolicies: []compiledPolicy{
				{method: "GET", pathPattern: "/", roles: []authPolicyRole{}},
			},
			tokenCache:        &tokenCache{ttl: time.Minute},
			tokenIntrospector: &mockIntrospector{},
		}
		req := httptest.NewRequest(http.MethodGet, "/", http.NoBody)
		w := httptest.NewRecorder()
		if !s.checkAuth(w, req) {
			t.Fatal("expected passthrough for public endpoint (empty roles)")
		}
	})
}

func TestCheckAuthMissingToken(t *testing.T) {
	s := &Shim{
		authPolicies: []compiledPolicy{
			{method: "*", pathPattern: "/*", roles: []authPolicyRole{{Name: "admin"}}},
		},
		tokenCache:        &tokenCache{ttl: time.Minute},
		tokenIntrospector: &mockIntrospector{},
	}
	req := httptest.NewRequest(http.MethodGet, "/resource_providers", http.NoBody)
	w := httptest.NewRecorder()
	if s.checkAuth(w, req) {
		t.Fatal("expected deny for missing token")
	}
	if w.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want %d", w.Code, http.StatusUnauthorized)
	}
}

func TestCheckAuthInvalidToken(t *testing.T) {
	s := &Shim{
		authPolicies: []compiledPolicy{
			{method: "*", pathPattern: "/*", roles: []authPolicyRole{{Name: "admin"}}},
		},
		tokenCache:        &tokenCache{ttl: time.Minute},
		tokenIntrospector: &mockIntrospector{err: errors.New("invalid token")},
	}
	req := httptest.NewRequest(http.MethodGet, "/traits", http.NoBody)
	req.Header.Set("X-Auth-Token", "bad-token")
	w := httptest.NewRecorder()
	if s.checkAuth(w, req) {
		t.Fatal("expected deny for invalid token")
	}
	if w.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want %d", w.Code, http.StatusUnauthorized)
	}
}

func TestCheckAuthValidToken(t *testing.T) {
	s := &Shim{
		authPolicies: []compiledPolicy{
			{method: "GET", pathPattern: "/*", roles: []authPolicyRole{
				{Name: "cloud_compute_admin"},
				{Name: "cloud_compute_viewer"},
			}},
		},
		tokenCache: &tokenCache{ttl: time.Minute},
		tokenIntrospector: &mockIntrospector{info: &tokenInfo{
			roles:     []string{"cloud_compute_viewer"},
			expiresAt: time.Now().Add(time.Hour),
			cachedAt:  time.Now(),
		}},
	}
	req := httptest.NewRequest(http.MethodGet, "/resource_providers", http.NoBody)
	req.Header.Set("X-Auth-Token", "good-token")
	w := httptest.NewRecorder()
	if !s.checkAuth(w, req) {
		t.Fatal("expected authorized")
	}
}

func TestCheckAuthInsufficientRoles(t *testing.T) {
	s := &Shim{
		authPolicies: []compiledPolicy{
			{method: "*", pathPattern: "/*", roles: []authPolicyRole{{Name: "cloud_compute_admin"}}},
		},
		tokenCache: &tokenCache{ttl: time.Minute},
		tokenIntrospector: &mockIntrospector{info: &tokenInfo{
			roles:     []string{"some_other_role"},
			expiresAt: time.Now().Add(time.Hour),
			cachedAt:  time.Now(),
		}},
	}
	req := httptest.NewRequest(http.MethodGet, "/traits", http.NoBody)
	req.Header.Set("X-Auth-Token", "token")
	w := httptest.NewRecorder()
	if s.checkAuth(w, req) {
		t.Fatal("expected deny for insufficient roles")
	}
	if w.Code != http.StatusForbidden {
		t.Errorf("status = %d, want %d", w.Code, http.StatusForbidden)
	}
}

func TestCheckAuthProjectScoped(t *testing.T) {
	policies := []compiledPolicy{
		{method: "GET", pathPattern: "/usages", roles: []authPolicyRole{
			{Name: "compute_viewer", ProjectScoped: true},
		}},
	}
	info := &tokenInfo{
		roles:     []string{"compute_viewer"},
		projectID: "proj-123",
		expiresAt: time.Now().Add(time.Hour),
		cachedAt:  time.Now(),
	}

	t.Run("matching project_id", func(t *testing.T) {
		s := &Shim{
			authPolicies:      policies,
			tokenCache:        &tokenCache{ttl: time.Minute},
			tokenIntrospector: &mockIntrospector{info: info},
		}
		req := httptest.NewRequest(http.MethodGet, "/usages?project_id=proj-123", http.NoBody)
		req.Header.Set("X-Auth-Token", "token")
		w := httptest.NewRecorder()
		if !s.checkAuth(w, req) {
			t.Fatal("expected authorized with matching project_id")
		}
	})

	t.Run("mismatched project_id", func(t *testing.T) {
		s := &Shim{
			authPolicies:      policies,
			tokenCache:        &tokenCache{ttl: time.Minute},
			tokenIntrospector: &mockIntrospector{info: info},
		}
		req := httptest.NewRequest(http.MethodGet, "/usages?project_id=other", http.NoBody)
		req.Header.Set("X-Auth-Token", "token")
		w := httptest.NewRecorder()
		if s.checkAuth(w, req) {
			t.Fatal("expected deny for mismatched project_id")
		}
		if w.Code != http.StatusForbidden {
			t.Errorf("status = %d, want %d", w.Code, http.StatusForbidden)
		}
	})

	t.Run("missing project_id", func(t *testing.T) {
		s := &Shim{
			authPolicies:      policies,
			tokenCache:        &tokenCache{ttl: time.Minute},
			tokenIntrospector: &mockIntrospector{info: info},
		}
		req := httptest.NewRequest(http.MethodGet, "/usages", http.NoBody)
		req.Header.Set("X-Auth-Token", "token")
		w := httptest.NewRecorder()
		if s.checkAuth(w, req) {
			t.Fatal("expected deny for missing project_id")
		}
		if w.Code != http.StatusForbidden {
			t.Errorf("status = %d, want %d", w.Code, http.StatusForbidden)
		}
	})
}

func TestCheckAuthFirstMatchWins(t *testing.T) {
	s := &Shim{
		authPolicies: []compiledPolicy{
			{method: "GET", pathPattern: "/usages", roles: nil}, // public
			{method: "*", pathPattern: "/*", roles: []authPolicyRole{{Name: "admin"}}},
		},
		tokenCache:        &tokenCache{ttl: time.Minute},
		tokenIntrospector: &mockIntrospector{},
	}
	req := httptest.NewRequest(http.MethodGet, "/usages", http.NoBody)
	// No token set — should still pass because first match is public.
	w := httptest.NewRecorder()
	if !s.checkAuth(w, req) {
		t.Fatal("expected first-match (public) to win")
	}
}

func TestCheckAuthCachesToken(t *testing.T) {
	mock := &mockIntrospector{info: &tokenInfo{
		roles:     []string{"admin"},
		expiresAt: time.Now().Add(time.Hour),
		cachedAt:  time.Now(),
	}}
	s := &Shim{
		authPolicies: []compiledPolicy{
			{method: "*", pathPattern: "/*", roles: []authPolicyRole{{Name: "admin"}}},
		},
		tokenCache:        &tokenCache{ttl: time.Minute},
		tokenIntrospector: mock,
	}
	for i := range 5 {
		req := httptest.NewRequest(http.MethodGet, "/traits", http.NoBody)
		req.Header.Set("X-Auth-Token", "same-token")
		w := httptest.NewRecorder()
		if !s.checkAuth(w, req) {
			t.Fatalf("request %d: expected authorized", i)
		}
	}
	if n := mock.calls.Load(); n != 1 {
		t.Errorf("introspect called %d times, want 1", n)
	}
}

func TestAuthErrorFormat(t *testing.T) {
	w := httptest.NewRecorder()
	authError(w, http.StatusUnauthorized, "Unauthorized",
		"The request you have made requires authentication.")

	if ct := w.Header().Get("Content-Type"); ct != "application/json" {
		t.Errorf("Content-Type = %q, want %q", ct, "application/json")
	}
	if w.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want %d", w.Code, http.StatusUnauthorized)
	}
	var body struct {
		Error struct {
			Code    int    `json:"code"`
			Title   string `json:"title"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("failed to parse response body: %v", err)
	}
	if body.Error.Code != 401 {
		t.Errorf("error code = %d, want 401", body.Error.Code)
	}
	if body.Error.Title != "Unauthorized" {
		t.Errorf("error title = %q, want %q", body.Error.Title, "Unauthorized")
	}
	if body.Error.Message != "The request you have made requires authentication." {
		t.Errorf("error message = %q", body.Error.Message)
	}
}
