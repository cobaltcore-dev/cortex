// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package supervisor

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"sync/atomic"
	"testing"
	"time"

	"k8s.io/apimachinery/pkg/util/wait"
)

// freeAddr returns a loopback address with a currently-free port. There is an
// inherent (small) race between closing the listener and the supervisor
// re-binding, but it is good enough for tests and avoids hardcoding ports.
func freeAddr(t *testing.T) string {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("failed to reserve a free port: %v", err)
	}
	addr := l.Addr().String()
	if err := l.Close(); err != nil {
		t.Fatalf("failed to close reservation listener: %v", err)
	}
	return addr
}

// runAsync runs the supervisor in a goroutine, failing the test if it returns
// an unexpected error.
func runAsync(t *testing.T, ctx context.Context, o Options) {
	t.Helper()
	go func() {
		if err := Run(ctx, o); err != nil {
			t.Errorf("Run returned error: %v", err)
		}
	}()
}

// getStatus issues a GET against the given address+path and returns the status
// code, retrying briefly to absorb the goroutine startup delay.
func getStatus(t *testing.T, addr, path string) int {
	t.Helper()
	var lastErr error
	for range 50 {
		resp, err := http.Get("http://" + addr + path) //nolint:noctx // test helper
		if err != nil {
			lastErr = err
			time.Sleep(20 * time.Millisecond)
			continue
		}
		if _, err := io.Copy(io.Discard, resp.Body); err != nil {
			t.Fatalf("failed to drain response body: %v", err)
		}
		if err := resp.Body.Close(); err != nil {
			t.Fatalf("failed to close response body: %v", err)
		}
		return resp.StatusCode
	}
	t.Fatalf("failed to reach %s%s: %v", addr, path, lastErr)
	return 0
}

func baseOptions(t *testing.T) Options {
	return Options{
		ProbeAddr: freeAddr(t),
		APIAddr:   freeAddr(t),
		Mux:       http.NewServeMux(),
	}
}

// TestRunRequiredOptions verifies Run rejects incomplete options without
// starting anything.
func TestRunRequiredOptions(t *testing.T) {
	cases := map[string]Options{
		"missing probe": {APIAddr: ":1", Mux: http.NewServeMux(), BuildAndStart: func(context.Context) error { return nil }},
		"missing api":   {ProbeAddr: ":1", Mux: http.NewServeMux(), BuildAndStart: func(context.Context) error { return nil }},
		"missing mux":   {ProbeAddr: ":1", APIAddr: ":2", BuildAndStart: func(context.Context) error { return nil }},
		"missing build": {ProbeAddr: ":1", APIAddr: ":2", Mux: http.NewServeMux()},
	}
	for name, o := range cases {
		t.Run(name, func(t *testing.T) {
			if err := Run(context.Background(), o); err == nil {
				t.Fatal("expected error for incomplete options, got nil")
			}
		})
	}
}

// TestProbeAlwaysOK verifies the liveness/readiness probe reports healthy even
// when the manager never starts successfully (BuildAndStart always errors).
func TestProbeAlwaysOK(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	o := baseOptions(t)
	o.BuildAndStart = func(ctx context.Context) error {
		// Never come up; block until cancelled so the loop keeps "failing".
		<-ctx.Done()
		return ctx.Err()
	}
	runAsync(t, ctx, o)

	if code := getStatus(t, o.ProbeAddr, "/healthz"); code != http.StatusOK {
		t.Errorf("/healthz = %d, want 200", code)
	}
	if code := getStatus(t, o.ProbeAddr, "/readyz"); code != http.StatusOK {
		t.Errorf("/readyz = %d, want 200", code)
	}
}

// TestAPIServedFromMux verifies routes registered on the mux are served by the
// outer API server, independent of the manager.
func TestAPIServedFromMux(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	o := baseOptions(t)
	o.Mux.HandleFunc("/ping", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusTeapot)
	})
	o.BuildAndStart = func(ctx context.Context) error { <-ctx.Done(); return nil }
	runAsync(t, ctx, o)

	if code := getStatus(t, o.APIAddr, "/ping"); code != http.StatusTeapot {
		t.Errorf("/ping = %d, want 418", code)
	}
}

// TestManagerRestartsOnFailure verifies a failing BuildAndStart is retried
// (the supervisor keeps the process alive and rebuilds the manager) rather than
// returning. The manager_up metric is owned by the caller (it needs to observe
// cache sync), so the supervisor is only responsible for the restart behavior.
func TestManagerRestartsOnFailure(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	var cycles atomic.Int32

	o := baseOptions(t)
	// Fast backoff so we do not slow the test down.
	o.Backoff = wait.Backoff{Duration: time.Millisecond, Factor: 1.0, Steps: 100}
	o.BuildAndStart = func(context.Context) error {
		cycles.Add(1)
		return errors.New("boom")
	}
	runAsync(t, ctx, o)

	// A failing manager must be rebuilt repeatedly, not cause Run to return.
	waitFor(t, func() bool { return cycles.Load() >= 3 }, "manager to be rebuilt after repeated failures")
}

// TestContextCancelStopsLoop verifies cancelling the context stops the loop and
// prevents any further manager rebuild.
func TestContextCancelStopsLoop(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())

	var cycles atomic.Int32
	done := make(chan error, 1)

	o := baseOptions(t)
	o.Backoff = wait.Backoff{Duration: 10 * time.Millisecond, Factor: 1.0, Steps: 100}
	o.BuildAndStart = func(context.Context) error {
		cycles.Add(1)
		return nil // return immediately so the loop spins on backoff
	}
	go func() { done <- Run(ctx, o) }()

	// Let a couple of cycles happen, then cancel.
	waitFor(t, func() bool { return cycles.Load() >= 1 }, "at least one manager cycle")
	cancel()

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Run returned error on cancel: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Run did not return after context cancel")
	}

	// No further rebuilds after Run returned.
	before := cycles.Load()
	time.Sleep(50 * time.Millisecond)
	if after := cycles.Load(); after != before {
		t.Errorf("manager rebuilt after loop exit: before=%d after=%d", before, after)
	}
}

// TestRunRequiresMetricsGathererWhenMetricsAddrSet verifies Run fails fast when
// a metrics bind address is configured without a gatherer, rather than silently
// serving no metrics.
func TestRunRequiresMetricsGathererWhenMetricsAddrSet(t *testing.T) {
	o := baseOptions(t)
	o.MetricsAddr = freeAddr(t)
	o.BuildAndStart = func(context.Context) error { return nil }
	if err := Run(context.Background(), o); err == nil {
		t.Fatal("expected error when MetricsAddr is set without MetricsGatherer, got nil")
	}
}

// TestOuterServerBindFailureIsFatal verifies that if an outer server cannot bind
// its port, Run returns an error instead of leaving the process "healthy" while
// serving nothing.
func TestOuterServerBindFailureIsFatal(t *testing.T) {
	// Occupy a port so the API server cannot bind to it.
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("failed to reserve port: %v", err)
	}
	defer func() {
		if err := l.Close(); err != nil {
			t.Errorf("failed to close listener: %v", err)
		}
	}()
	occupied := l.Addr().String()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	o := baseOptions(t)
	o.APIAddr = occupied
	o.BuildAndStart = func(ctx context.Context) error { <-ctx.Done(); return nil }

	done := make(chan error, 1)
	go func() { done <- Run(ctx, o) }()

	select {
	case err := <-done:
		if err == nil {
			t.Fatal("expected Run to return an error on bind failure, got nil")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Run did not return after outer server bind failure")
	}
}

// TestBackoffProgressionAndReset verifies the delay grows across failures and
// that runManagerCycle resets the backoff after the manager stays up past the
// healthy threshold.
func TestBackoffProgressionAndReset(t *testing.T) {
	// Progression: successive Step() calls grow.
	b := wait.Backoff{Duration: 10 * time.Millisecond, Factor: 2.0, Steps: 100}
	first := b.Step()
	second := b.Step()
	if second <= first {
		t.Errorf("expected backoff to grow: first=%v second=%v", first, second)
	}

	// Reset: after a manager stays up longer than healthyResetAfter,
	// runManagerCycle resets the working backoff to the configured Options.Backoff
	// so a later outage starts from the initial delay again.
	initial := wait.Backoff{Duration: 10 * time.Millisecond, Factor: 2.0, Steps: 100}
	o := &Options{Backoff: initial}
	working := initial
	working.Step() // advance it so a no-op reset would be observable
	working.Step()
	const healthyResetAfter = 20 * time.Millisecond
	o.BuildAndStart = func(context.Context) error {
		time.Sleep(2 * healthyResetAfter) // stay "up" past the threshold
		return nil
	}
	runManagerCycle(context.Background(), o, healthyResetAfter, &working)
	// After a healthy cycle, the working backoff should equal a fresh initial
	// backoff: its first Step() must match the first Step() of the initial.
	wantFirst := initial
	if got, want := working.Step(), wantFirst.Step(); got != want {
		t.Errorf("backoff not reset after healthy cycle: first step=%v, want %v", got, want)
	}

	// And a cycle that returns quickly (before the threshold) must NOT reset.
	working2 := initial
	working2.Step()
	advanced := working2 // snapshot of the advanced state
	o.BuildAndStart = func(context.Context) error { return errors.New("boom") }
	runManagerCycle(context.Background(), o, time.Hour, &working2)
	if got, want := working2.Step(), advanced.Step(); got != want {
		t.Errorf("backoff reset after a short cycle: first step=%v, want %v", got, want)
	}
}

// TestManagerRestartsOnInternalCycleCancel locks down the exact mechanism the
// placement shim relies on: BuildAndStart derives a per-cycle child context from
// the one the supervisor passes in and cancels it itself (as the shim does when a
// remote apiserver goes unreachable), then returns. The parent context is still
// live, so the supervisor must treat this as a manager exit and rebuild — not
// mistake it for a graceful shutdown and stop the loop.
func TestManagerRestartsOnInternalCycleCancel(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	var cycles atomic.Int32

	o := baseOptions(t)
	o.Backoff = wait.Backoff{Duration: time.Millisecond, Factor: 1.0, Steps: 100}
	o.BuildAndStart = func(parent context.Context) error {
		cycles.Add(1)
		// Mirror main.go's buildAndStart: derive a per-cycle context and cancel
		// it from within (as the reachability probe does), then return its error.
		cycleCtx, cancelCycle := context.WithCancel(parent)
		cancelCycle()
		return cycleCtx.Err()
	}
	runAsync(t, ctx, o)

	// A cycle that self-cancels and returns while the parent is live must be
	// rebuilt, not end the supervision loop.
	waitFor(t, func() bool { return cycles.Load() >= 3 }, "manager to be rebuilt after an internal per-cycle cancel")
}

// waitFor polls cond up to ~2s, failing the test if it never becomes true.
func waitFor(t *testing.T, cond func() bool, what string) {
	t.Helper()
	for range 200 {
		if cond() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}
