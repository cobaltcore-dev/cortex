// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

// Package supervisor runs a self-healing controller-manager for a shim while
// keeping the shim's HTTP surface (REST API, liveness probe, metrics) alive and
// decoupled from the manager's lifecycle.
//
// A shim such as the placement shim must keep serving even when its
// controller-runtime manager loses connectivity to a (local or remote)
// apiserver. In controller-runtime, the manager owns the informer cache and,
// crucially, every long-lived HTTP server it hosts (healthz, metrics, webhook)
// as Runnables — when the manager stops, those servers stop too, and
// mgr.Start returns an error. Coupling the process to that means a connectivity
// blip crashes the pod.
//
// The supervisor breaks that coupling:
//
//   - It binds the liveness/readiness probe, the REST API mux, and (optionally)
//     the metrics endpoint ONCE, in the outer process, so they survive across
//     manager restarts. The probe always reports healthy once the process is
//     up, so pod liveness reflects the process, not the apiserver.
//   - It runs a supervision loop that builds and starts a fresh manager (and
//     its caches) via a caller-supplied BuildAndStart function, and restarts it
//     with capped, jittered backoff whenever it returns. A looping manager is
//     surfaced via the ManagerUp gauge (1 while a manager is running, 0
//     otherwise) rather than by crashing the pod.
//
// The package is deliberately shim-agnostic so future shims can reuse it.
package supervisor

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"github.com/sapcc/go-bits/httpext"
	"k8s.io/apimachinery/pkg/util/wait"
	ctrl "sigs.k8s.io/controller-runtime"
)

var log = ctrl.Log.WithName("shim-supervisor")

// Options configures a supervisor run. The three HTTP servers are bound once
// for the whole process lifetime; only BuildAndStart is invoked repeatedly.
type Options struct {
	// ProbeAddr is the address the liveness/readiness probe binds to (e.g.
	// ":8081"). It always reports healthy (200) once the process is up,
	// decoupling pod liveness from apiserver/cache state. Required.
	ProbeAddr string
	// APIAddr is the address the REST API binds to (e.g. ":8080"). Required.
	APIAddr string
	// MetricsAddr is the address the metrics endpoint binds to (e.g. ":2112").
	// If empty or "0", no outer metrics server is started (the caller may leave
	// metrics to the manager, or disable them). Served from MetricsGatherer so
	// metrics stay scrapable across manager restarts.
	MetricsAddr string
	// MetricsGatherer is scraped by the outer metrics server. Typically the
	// global controller-runtime metrics registry. Required when MetricsAddr is
	// set to a real address; ignored otherwise.
	MetricsGatherer prometheus.Gatherer
	// Mux serves the REST API on APIAddr. Its routes must be registered before
	// Run is called; the supervisor never mutates it. Required.
	Mux *http.ServeMux
	// ManagerUp is set to 1 while a manager is running and 0 between restarts.
	// Optional; nil disables the gauge.
	ManagerUp prometheus.Gauge
	// Backoff controls the wait between manager restarts. The zero value uses
	// DefaultBackoff. The backoff is reset after a manager has stayed up longer
	// than HealthyResetAfter, so a brief blip does not inflate the delay before
	// the next real outage.
	Backoff wait.Backoff
	// HealthyResetAfter is how long a manager must stay up before its backoff is
	// reset. The zero value uses DefaultHealthyResetAfter.
	HealthyResetAfter time.Duration
	// BuildAndStart builds a fresh manager (and its caches/controllers) and runs
	// it, blocking until it stops. It receives a child context that is cancelled
	// when the outer context is cancelled. It should return the error from
	// mgr.Start (or a build error) so the supervisor can back off and retry.
	// Required.
	BuildAndStart func(ctx context.Context) error
}

// DefaultBackoff is a capped exponential backoff with jitter used when
// Options.Backoff is the zero value.
var DefaultBackoff = wait.Backoff{
	Duration: 1 * time.Second,
	Factor:   2.0,
	Jitter:   0.2,
	Cap:      30 * time.Second,
	Steps:    100,
}

// DefaultHealthyResetAfter is the default for Options.HealthyResetAfter.
const DefaultHealthyResetAfter = 60 * time.Second

// Run binds the outer HTTP servers once and then supervises the manager until
// ctx is cancelled. It returns nil on graceful shutdown (ctx cancelled) and an
// error only if the required options are missing.
func Run(ctx context.Context, o Options) error {
	if o.ProbeAddr == "" || o.APIAddr == "" || o.Mux == nil || o.BuildAndStart == nil {
		return errors.New("supervisor: ProbeAddr, APIAddr, Mux and BuildAndStart are required")
	}
	metricsEnabled := o.MetricsAddr != "" && o.MetricsAddr != "0"
	if metricsEnabled && o.MetricsGatherer == nil {
		return errors.New("supervisor: MetricsGatherer is required when MetricsAddr is set")
	}
	backoff := o.Backoff
	if backoff.Duration == 0 {
		backoff = DefaultBackoff
	}
	healthyResetAfter := o.HealthyResetAfter
	if healthyResetAfter == 0 {
		healthyResetAfter = DefaultHealthyResetAfter
	}

	// serverErr carries a bind/serve failure from any outer server. A failure to
	// open a port (e.g. address in use) is fatal — even while a manager is
	// running healthily — rather than leaving the process "healthy" while serving
	// nothing. A clean exit on ctx cancellation reports nil.
	serverErr := make(chan error, 3)
	serve := func(name, addr string, handler http.Handler) {
		go func() {
			log.Info("starting "+name+" server", "address", addr)
			err := httpext.ListenAndServeContext(ctx, addr, handler)
			if err != nil && ctx.Err() == nil {
				log.Error(err, name+" server exited with error")
				serverErr <- err
				return
			}
			serverErr <- nil
		}()
	}

	// loopCtx is cancelled either when the outer ctx is cancelled or when an
	// outer server fails, so a bind/serve failure also tears down a running
	// manager instead of waiting for it to exit on its own.
	loopCtx, cancelLoop := context.WithCancel(ctx)
	defer cancelLoop()
	var fatalErr error
	go func() {
		if err := <-serverErr; err != nil {
			fatalErr = err
			cancelLoop()
		}
	}()

	// Bind the liveness/readiness probe once. It always reports healthy so pod
	// liveness reflects the process being up, not apiserver reachability.
	probeMux := http.NewServeMux()
	alwaysOK := func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}
	probeMux.HandleFunc("/healthz", alwaysOK)
	probeMux.HandleFunc("/readyz", alwaysOK)
	serve("liveness probe", o.ProbeAddr, probeMux)

	// Bind the REST API once so it survives manager restarts.
	serve("api", o.APIAddr, o.Mux)

	// Bind the metrics server once (if configured) so metrics — including
	// ManagerUp — stay scrapable during a manager restart.
	if metricsEnabled {
		metricsMux := http.NewServeMux()
		metricsMux.Handle("/metrics", promhttp.HandlerFor(
			o.MetricsGatherer, promhttp.HandlerOpts{},
		))
		serve("metrics", o.MetricsAddr, metricsMux)
	}

	// Supervision loop: (re)build and start the manager, backing off on each
	// return, until the outer context is cancelled or an outer server fails to
	// bind/serve.
	for {
		// Observe cancellation before doing any work so we never rebuild a manager
		// during shutdown or after a fatal outer-server failure.
		select {
		case <-loopCtx.Done():
			if fatalErr != nil {
				return fmt.Errorf("supervisor: outer server failed: %w", fatalErr)
			}
			return nil
		default:
		}

		runManagerCycle(loopCtx, &o, healthyResetAfter, &backoff)

		delay := backoff.Step()
		log.Info("manager stopped, backing off before restart", "delay", delay.String())
		select {
		case <-loopCtx.Done():
			if fatalErr != nil {
				return fmt.Errorf("supervisor: outer server failed: %w", fatalErr)
			}
			return nil
		case <-time.After(delay):
		}
	}
}

// runManagerCycle runs a single manager lifecycle: it sets the ManagerUp gauge,
// runs BuildAndStart to completion, and clears the gauge on return. If the
// manager stayed up longer than healthyResetAfter, the backoff is reset so the
// next outage starts from the initial delay.
func runManagerCycle(ctx context.Context, o *Options, healthyResetAfter time.Duration, backoff *wait.Backoff) {
	if o.ManagerUp != nil {
		o.ManagerUp.Set(1)
		defer o.ManagerUp.Set(0)
	}
	start := time.Now()
	log.Info("starting manager")
	if err := o.BuildAndStart(ctx); err != nil {
		log.Error(err, "manager exited with error; will restart")
	} else {
		log.Info("manager exited")
	}
	if time.Since(start) >= healthyResetAfter {
		reset := o.Backoff
		if reset.Duration == 0 {
			reset = DefaultBackoff
		}
		*backoff = reset
	}
}
