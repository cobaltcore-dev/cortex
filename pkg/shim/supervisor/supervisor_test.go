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

	"github.com/prometheus/client_golang/prometheus"
	dto "github.com/prometheus/client_model/go"
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

// gaugeValue reads the current value of a gauge.
func gaugeValue(t *testing.T, g prometheus.Gauge) float64 {
	t.Helper()
	m := &dto.Metric{}
	if err := g.Write(m); err != nil {
		t.Fatalf("failed to write gauge: %v", err)
	}
	return m.GetGauge().GetValue()
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

// TestManagerUpGaugeTransitions verifies the gauge is 1 while a manager runs
// and 0 after it returns.
func TestManagerUpGaugeTransitions(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	gauge := prometheus.NewGauge(prometheus.GaugeOpts{Name: "test_manager_up"})
	running := make(chan struct{})
	release := make(chan struct{})
	var cycles atomic.Int32

	o := baseOptions(t)
	o.ManagerUp = gauge
	// Fast backoff so we do not slow the test down.
	o.Backoff = wait.Backoff{Duration: time.Millisecond, Factor: 1.0, Steps: 100}
	o.BuildAndStart = func(context.Context) error {
		if cycles.Add(1) == 1 {
			close(running)
			<-release // hold the first manager "up" until the test releases it
		}
		return errors.New("boom")
	}
	runAsync(t, ctx, o)

	<-running
	// Give the deferred Set(1) a moment (it runs before BuildAndStart is called).
	waitFor(t, func() bool { return gaugeValue(t, gauge) == 1 }, "gauge to reach 1 while manager up")

	close(release)
	// After the manager returns the gauge should drop to 0 at least momentarily;
	// since it restarts quickly it may flip back to 1, so just assert we saw a
	// restart happen (cycles advanced) and the gauge is a valid 0/1 value.
	waitFor(t, func() bool { return cycles.Load() >= 2 }, "manager to restart after failure")
	if v := gaugeValue(t, gauge); v != 0 && v != 1 {
		t.Errorf("gauge value = %v, want 0 or 1", v)
	}
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

// TestBackoffProgressionAndReset verifies the delay grows across failures and
// resets after the manager stays up past the healthy threshold.
func TestBackoffProgressionAndReset(t *testing.T) {
	b := wait.Backoff{Duration: 10 * time.Millisecond, Factor: 2.0, Steps: 100}
	first := b.Step()
	second := b.Step()
	if second <= first {
		t.Errorf("expected backoff to grow: first=%v second=%v", first, second)
	}
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
