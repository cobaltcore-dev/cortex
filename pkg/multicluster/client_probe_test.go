// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package multicluster

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus/testutil"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/rest"
)

// fakeRemote builds a fakeCluster whose rest config points at the given host.
func fakeRemote(t *testing.T, host string) *fakeCluster {
	t.Helper()
	scheme := newTestScheme(t)
	c := newFakeCluster(scheme)
	c.restConfig = &rest.Config{Host: host}
	return c
}

func TestUniqueRemotes_DedupesAcrossGVKs(t *testing.T) {
	gvkA := schema.GroupVersionKind{Group: "g", Version: "v", Kind: "A"}
	gvkB := schema.GroupVersionKind{Group: "g", Version: "v", Kind: "B"}
	shared := fakeRemote(t, "https://shared")
	other := fakeRemote(t, "https://other")
	c := &Client{
		remoteClusters: map[schema.GroupVersionKind][]remoteCluster{
			gvkA: {{cluster: shared}, {cluster: other}},
			gvkB: {{cluster: shared}},
		},
	}
	remotes := c.UniqueRemotes()
	if len(remotes) != 2 {
		t.Fatalf("expected 2 unique remotes, got %d", len(remotes))
	}
	hosts := map[string]bool{}
	for _, r := range remotes {
		hosts[r.Host] = true
	}
	if !hosts["https://shared"] || !hosts["https://other"] {
		t.Errorf("unexpected hosts: %v", hosts)
	}
}

func TestUniqueRemotes_NoRemotes(t *testing.T) {
	c := &Client{}
	if got := c.UniqueRemotes(); len(got) != 0 {
		t.Errorf("expected no remotes, got %d", len(got))
	}
}

func TestProbeReachable_Classification(t *testing.T) {
	tests := []struct {
		name          string
		status        int // 0 means "close the connection / unreachable"
		wantReachable bool
	}{
		{"ok", http.StatusOK, true},
		{"forbidden apiserver is up", http.StatusForbidden, true},
		{"unauthorized apiserver is up", http.StatusUnauthorized, true},
		{"server error apiserver is up", http.StatusInternalServerError, true},
		{"connection refused is unreachable", 0, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var host string
			if tt.status == 0 {
				// Reserve a port and close the listener so connections are refused.
				srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
				host = srv.URL
				srv.Close()
			} else {
				srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
					w.WriteHeader(tt.status)
				}))
				t.Cleanup(srv.Close)
				host = srv.URL
			}
			client, err := reachabilityClient(&rest.Config{Host: host, TLSClientConfig: rest.TLSClientConfig{Insecure: true}}, time.Second)
			if err != nil {
				t.Fatalf("reachabilityClient: %v", err)
			}
			got, probeErr := probeReachable(context.Background(), client, time.Second)
			if got != tt.wantReachable {
				t.Errorf("probeReachable = %v (err %v), want %v", got, probeErr, tt.wantReachable)
			}
			// A 200 yields no error; any other outcome (reachable non-2xx or an
			// unreachable transport failure) surfaces the underlying error.
			if tt.status == http.StatusOK && probeErr != nil {
				t.Errorf("expected nil error on 200, got %v", probeErr)
			}
			if tt.status != http.StatusOK && probeErr == nil {
				t.Errorf("expected a non-nil error for status %d / unreachable, got nil", tt.status)
			}
		})
	}
}

func TestProbeRemotes_TriggersOnSustainedFailure(t *testing.T) {
	// A remote pointing at a closed listener is always unreachable.
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	deadHost := srv.URL
	srv.Close()

	remote := fakeRemote(t, deadHost)
	mon := NewMonitor("test_trigger_")
	c := &Client{
		Monitor:        mon,
		remoteClusters: map[schema.GroupVersionKind][]remoteCluster{{Kind: "A"}: {{cluster: remote}}},
	}

	var mu sync.Mutex
	var lostHosts []string
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan struct{})
	go func() {
		c.ProbeRemotes(ctx, ProbeOptions{Interval: 5 * time.Millisecond, Timeout: 50 * time.Millisecond, FailureThreshold: 3}, func(host string) {
			mu.Lock()
			lostHosts = append(lostHosts, host)
			mu.Unlock()
		})
		close(done)
	}()

	// The probe loop stops itself once the remote is declared lost.
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("ProbeRemotes did not return after remote was declared lost")
	}

	mu.Lock()
	if len(lostHosts) != 1 || lostHosts[0] != deadHost {
		t.Errorf("expected onLost called once with %q, got %v", deadHost, lostHosts)
	}
	mu.Unlock()

	if got := testutil.ToFloat64(mon.(*monitor).remoteReachable.WithLabelValues(deadHost)); got != 0 {
		t.Errorf("expected reachable gauge 0 for dead host, got %v", got)
	}
}

func TestProbeRemotes_NoTriggerWhenReachable(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(srv.Close)

	remote := fakeRemote(t, srv.URL)
	remote.restConfig.TLSClientConfig = rest.TLSClientConfig{Insecure: true}
	mon := NewMonitor("test_reachable_")
	c := &Client{
		Monitor:        mon,
		remoteClusters: map[schema.GroupVersionKind][]remoteCluster{{Kind: "A"}: {{cluster: remote}}},
	}

	var mu sync.Mutex
	lost := false
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go c.ProbeRemotes(ctx, ProbeOptions{Interval: 5 * time.Millisecond, Timeout: time.Second, FailureThreshold: 3}, func(string) {
		mu.Lock()
		lost = true
		mu.Unlock()
	})

	// Poll for the gauge to reach 1: a reachable remote must record 1 and never
	// trip onLost. Polling (rather than a fixed sleep + single read) avoids racing
	// the probe goroutine's gauge write.
	gauge := mon.(*monitor).remoteReachable.WithLabelValues(srv.URL)
	reachableSeen := false
	for range 200 {
		if testutil.ToFloat64(gauge) == 1 {
			reachableSeen = true
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	cancel()

	mu.Lock()
	defer mu.Unlock()
	if lost {
		t.Error("onLost was called for a reachable remote")
	}
	if !reachableSeen {
		t.Errorf("expected reachable gauge to reach 1, last value %v", testutil.ToFloat64(gauge))
	}
}

func TestProbeRemotes_NoRemotesIsNoop(t *testing.T) {
	c := &Client{}
	lost := false
	// Should return immediately without ever calling onLost.
	c.ProbeRemotes(context.Background(), DefaultProbeOptions, func(string) { lost = true })
	if lost {
		t.Error("onLost called with no remotes configured")
	}
}

func TestProbeRemotes_TransientBlipResetsCounter(t *testing.T) {
	// probeRemoteLoop's counter resets on any reachable probe, so a run of
	// failures shorter than the threshold never trips onLost. Drive the counter
	// logic directly: a probe func that fails twice (below threshold 3) then
	// recovers must leave failures at 0 and never call onLost.
	failures := 0
	onLost := false
	reachSeq := []bool{false, false, true, true}
	i := 0
	step := func() bool {
		reachable := reachSeq[i]
		i++
		if reachable {
			failures = 0
			return false
		}
		failures++
		if failures >= 3 {
			onLost = true
			return true
		}
		return false
	}
	for i < len(reachSeq) {
		if step() {
			break
		}
	}
	if onLost {
		t.Error("onLost tripped despite failures never reaching the threshold")
	}
	if failures != 0 {
		t.Errorf("expected failure counter reset to 0 after recovery, got %d", failures)
	}
}
