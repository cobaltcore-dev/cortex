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

// newDomainResolverTestServer creates an httptest.Server that serves Keystone
// GET /domains/{id} responses. The provided map controls what is returned for
// each domain ID. Unknown IDs get a 404.
func newDomainResolverTestServer(t *testing.T, domainsByID map[string]string) (*httptest.Server, *atomic.Int32) {
	t.Helper()
	var callCount atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Keystone endpoint is registered as the base URL, so gophercloud appends
		// the path "/domains/<id>" directly.
		// Path format: /domains/<id>
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
		_ = json.NewEncoder(w).Encode(map[string]any{
			"domain": map[string]any{"id": id, "name": name},
		})
	}))
	return server, &callCount
}

// newTestServiceClient builds a gophercloud.ServiceClient whose Endpoint points
// at the given test server URL (with trailing slash as gophercloud expects).
func newTestServiceClient(serverURL string) *gophercloud.ServiceClient {
	provider := &gophercloud.ProviderClient{}
	return &gophercloud.ServiceClient{
		ProviderClient: provider,
		Endpoint:       serverURL + "/",
		Type:           "identity",
	}
}

func TestKeystoneDomainResolver_ResolveDomainName(t *testing.T) {
	server, callCount := newDomainResolverTestServer(t, map[string]string{
		"domain-abc": "monsoon3",
		"domain-xyz": "cc3test",
	})
	defer server.Close()

	resolver := newKeystoneDomainResolver(newTestServiceClient(server.URL))

	name, err := resolver.ResolveDomainName(context.Background(), "domain-abc")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if name != "monsoon3" {
		t.Errorf("expected %q, got %q", "monsoon3", name)
	}
	if callCount.Load() != 1 {
		t.Errorf("expected 1 Keystone call, got %d", callCount.Load())
	}
}

func TestKeystoneDomainResolver_CacheHit(t *testing.T) {
	server, callCount := newDomainResolverTestServer(t, map[string]string{
		"domain-abc": "monsoon3",
	})
	defer server.Close()

	resolver := newKeystoneDomainResolver(newTestServiceClient(server.URL))

	for i := range 5 {
		name, err := resolver.ResolveDomainName(context.Background(), "domain-abc")
		if err != nil {
			t.Fatalf("call %d: unexpected error: %v", i, err)
		}
		if name != "monsoon3" {
			t.Errorf("call %d: expected %q, got %q", i, "monsoon3", name)
		}
	}

	// Only one HTTP request should have been made regardless of call count.
	if callCount.Load() != 1 {
		t.Errorf("expected exactly 1 Keystone call due to caching, got %d", callCount.Load())
	}
}

func TestKeystoneDomainResolver_MultipleDomains(t *testing.T) {
	server, _ := newDomainResolverTestServer(t, map[string]string{
		"domain-a": "alpha",
		"domain-b": "beta",
	})
	defer server.Close()

	resolver := newKeystoneDomainResolver(newTestServiceClient(server.URL))

	for _, tc := range []struct{ id, want string }{
		{"domain-a", "alpha"},
		{"domain-b", "beta"},
	} {
		name, err := resolver.ResolveDomainName(context.Background(), tc.id)
		if err != nil {
			t.Errorf("domain %q: unexpected error: %v", tc.id, err)
			continue
		}
		if name != tc.want {
			t.Errorf("domain %q: expected %q, got %q", tc.id, tc.want, name)
		}
	}
}

func TestKeystoneDomainResolver_NotFound(t *testing.T) {
	server, _ := newDomainResolverTestServer(t, map[string]string{})
	defer server.Close()

	resolver := newKeystoneDomainResolver(newTestServiceClient(server.URL))

	_, err := resolver.ResolveDomainName(context.Background(), "nonexistent")
	if err == nil {
		t.Fatal("expected an error for unknown domain, got nil")
	}
}

func TestKeystoneDomainResolver_ErrorNotCached(t *testing.T) {
	// First request fails (5xx), second request succeeds.
	// The resolver must NOT cache the error, so the second call retries Keystone
	// and returns the correct name.
	var callCount atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := callCount.Add(1)
		if n == 1 {
			http.Error(w, "internal server error", http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"domain": map[string]any{"id": "domain-flaky", "name": "recovered"},
		})
	}))
	defer server.Close()

	resolver := newKeystoneDomainResolver(newTestServiceClient(server.URL))

	_, err := resolver.ResolveDomainName(context.Background(), "domain-flaky")
	if err == nil {
		t.Fatal("expected error on first (failing) call, got nil")
	}

	name, err := resolver.ResolveDomainName(context.Background(), "domain-flaky")
	if err != nil {
		t.Fatalf("expected success on second call after Keystone recovered, got: %v", err)
	}
	if name != "recovered" {
		t.Errorf("expected %q, got %q", "recovered", name)
	}
	if callCount.Load() != 2 {
		t.Errorf("expected 2 Keystone calls (error not cached), got %d", callCount.Load())
	}
}

func TestKeystoneDomainResolver_ErrorWrapping(t *testing.T) {
	server, _ := newDomainResolverTestServer(t, map[string]string{})
	defer server.Close()

	resolver := newKeystoneDomainResolver(newTestServiceClient(server.URL))

	_, err := resolver.ResolveDomainName(context.Background(), "missing-domain")
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	// The error message must include the domain ID so callers can diagnose which
	// lookup failed without having to add their own context.
	if !strings.Contains(err.Error(), "missing-domain") {
		t.Errorf("expected error to contain domain ID %q, got: %v", "missing-domain", err)
	}
}

func TestKeystoneDomainResolver_ContextCancelled(t *testing.T) {
	// Server that blocks until the context is cancelled.
	blocked := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-blocked // never unblocked; request hangs until client cancels
	}))
	defer server.Close()
	defer close(blocked)

	resolver := newKeystoneDomainResolver(newTestServiceClient(server.URL))

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // cancel immediately

	_, err := resolver.ResolveDomainName(ctx, "domain-cancelled")
	if err == nil {
		t.Fatal("expected error when context is cancelled, got nil")
	}
}

func TestKeystoneDomainResolver_ConcurrentAccess(t *testing.T) {
	server, callCount := newDomainResolverTestServer(t, map[string]string{
		"domain-concurrent": "shared",
	})
	defer server.Close()

	resolver := newKeystoneDomainResolver(newTestServiceClient(server.URL))

	const goroutines = 20
	errs := make(chan error, goroutines)
	var wg sync.WaitGroup
	for range goroutines {
		wg.Add(1)
		go func() {
			defer wg.Done()
			name, err := resolver.ResolveDomainName(context.Background(), "domain-concurrent")
			if err != nil {
				errs <- err
				return
			}
			if name != "shared" {
				errs <- fmt.Errorf("expected %q, got %q", "shared", name)
			}
		}()
	}
	wg.Wait()
	close(errs)

	for err := range errs {
		t.Errorf("concurrent call error: %v", err)
	}

	// Due to the double-checked-lock pattern, at most a small number of calls
	// may race before the cache is warm. The important property is correctness:
	// all goroutines must have received the right answer.
	// We allow up to goroutines calls in the absolute worst case (no caching),
	// but the real invariant is just that no data races occurred (enforced by -race).
	if n := callCount.Load(); n > int32(goroutines) {
		t.Errorf("unexpectedly high Keystone call count: %d", n)
	}
}
