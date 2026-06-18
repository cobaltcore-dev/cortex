// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package commitments

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/gophercloud/gophercloud/v2"
)

// newDomainResolverTestServer creates an httptest.Server serving Keystone
// GET /domains/{id} responses. Unknown IDs get 404.
func newDomainResolverTestServer(t *testing.T, domainsByID map[string]string) (*httptest.Server, *atomic.Int32) {
	t.Helper()
	var callCount atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		const prefix = "/domains/"
		if len(r.URL.Path) <= len(prefix) {
			http.Error(w, "bad path", http.StatusBadRequest)
			return
		}
		id := r.URL.Path[len(prefix):]
		callCount.Add(1)
		name, ok := domainsByID[id]
		if !ok {
			http.Error(w, fmt.Sprintf("domain %q not found", id), http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"domain": map[string]any{"id": id, "name": name}})
	}))
	return server, &callCount
}

// newTestServiceClient builds a gophercloud.ServiceClient pointing at serverURL.
func newTestServiceClient(serverURL string) *gophercloud.ServiceClient {
	return &gophercloud.ServiceClient{
		ProviderClient: &gophercloud.ProviderClient{},
		Endpoint:       serverURL + "/",
		Type:           "identity",
	}
}

func TestKeystoneDomainResolver(t *testing.T) {
	server, callCount := newDomainResolverTestServer(t, map[string]string{
		"domain-a": "alpha",
		"domain-b": "beta",
	})
	defer server.Close()
	resolver := newKeystoneDomainResolver(newTestServiceClient(server.URL))

	tests := []struct {
		name        string
		domainID    string
		wantName    string
		wantErr     bool
		errContains string
	}{
		{name: "resolves domain name", domainID: "domain-a", wantName: "alpha"},
		{name: "resolves second domain independently", domainID: "domain-b", wantName: "beta"},
		{name: "not found returns error", domainID: "nonexistent", wantErr: true},
		{name: "error contains domain ID", domainID: "missing-domain", wantErr: true, errContains: "missing-domain"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			name, err := resolver.ResolveDomainName(context.Background(), tt.domainID)
			if tt.wantErr {
				if err == nil {
					t.Fatal("expected error, got nil")
				}
				if tt.errContains != "" && !strings.Contains(err.Error(), tt.errContains) {
					t.Errorf("expected error to contain %q, got: %v", tt.errContains, err)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if name != tt.wantName {
				t.Errorf("expected %q, got %q", tt.wantName, name)
			}
		})
	}

	// Each successful domain ID fetched exactly once; subsequent calls served from cache.
	// Error cases (not_found, missing-domain) also hit the server but must not be cached.
	// We have 2 success lookups (domain-a, domain-b) + 2 error lookups = 4 total calls.
	if n := callCount.Load(); n != 4 {
		t.Errorf("expected 4 Keystone calls (2 success + 2 error), got %d", n)
	}

	t.Run("cache hit does not re-fetch", func(t *testing.T) {
		before := callCount.Load()
		if _, err := resolver.ResolveDomainName(context.Background(), "domain-a"); err != nil {
			t.Fatalf("unexpected error on cache hit: %v", err)
		}
		if after := callCount.Load(); after != before {
			t.Errorf("expected no additional Keystone calls on cache hit, got %d new call(s)", after-before)
		}
	})
}

func TestKeystoneDomainResolver_ErrorNotCached(t *testing.T) {
	// First call fails (5xx); second must retry and succeed — errors must not be cached.
	var callCount atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if callCount.Add(1) == 1 {
			http.Error(w, "internal server error", http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"domain": map[string]any{"id": "domain-flaky", "name": "recovered"}})
	}))
	defer server.Close()
	resolver := newKeystoneDomainResolver(newTestServiceClient(server.URL))

	if _, err := resolver.ResolveDomainName(context.Background(), "domain-flaky"); err == nil {
		t.Fatal("expected error on first (failing) call, got nil")
	}
	name, err := resolver.ResolveDomainName(context.Background(), "domain-flaky")
	if err != nil {
		t.Fatalf("expected success after Keystone recovered, got: %v", err)
	}
	if name != "recovered" {
		t.Errorf("expected %q, got %q", "recovered", name)
	}
	if callCount.Load() != 2 {
		t.Errorf("expected 2 Keystone calls (error not cached), got %d", callCount.Load())
	}
}

func TestKeystoneDomainResolver_ContextCancelled(t *testing.T) {
	blocked := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-blocked
	}))
	defer server.Close()
	defer close(blocked)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := newKeystoneDomainResolver(newTestServiceClient(server.URL)).ResolveDomainName(ctx, "domain-x")
	if err == nil {
		t.Fatal("expected error when context is cancelled, got nil")
	}
}

func TestKeystoneDomainResolver_ConcurrentAccess(t *testing.T) {
	server, callCount := newDomainResolverTestServer(t, map[string]string{"domain-c": "shared"})
	defer server.Close()
	resolver := newKeystoneDomainResolver(newTestServiceClient(server.URL))

	const goroutines = 20
	errs := make(chan error, goroutines)
	var wg sync.WaitGroup
	for range goroutines {
		wg.Add(1)
		go func() {
			defer wg.Done()
			name, err := resolver.ResolveDomainName(context.Background(), "domain-c")
			if err != nil {
				errs <- err
			} else if name != "shared" {
				errs <- fmt.Errorf("expected %q, got %q", "shared", name)
			}
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Errorf("concurrent call error: %v", err)
	}
	if n := callCount.Load(); n > int32(goroutines) {
		t.Errorf("unexpectedly high Keystone call count: %d", n)
	}
}
