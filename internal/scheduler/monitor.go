// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package scheduler

import (
	"log/slog"
	"net/http"
	"strconv"
	"time"

	"github.com/cobaltcore-dev/cortex/internal/monitoring"
	"github.com/prometheus/client_golang/prometheus"
)

// Collection of Prometheus metrics to monitor scheduler pipeline
type APIMonitor struct {
	// A histogram to measure how long the API requests take to run.
	ApiRequestsTimer *prometheus.HistogramVec
}

// Create a new scheduler monitor and register the necessary Prometheus metrics.
func NewSchedulerMonitor(registry *monitoring.Registry) APIMonitor {
	apiRequestsTimer := prometheus.NewHistogramVec(prometheus.HistogramOpts{
		Name:    "cortex_scheduler_api_request_duration_seconds",
		Help:    "Duration of API requests",
		Buckets: prometheus.DefBuckets,
	}, []string{"method", "path", "status", "error"})
	registry.MustRegister(
		apiRequestsTimer,
	)
	return APIMonitor{
		ApiRequestsTimer: apiRequestsTimer,
	}
}

// Helper to respond to the request with the given code and error.
// Adds monitoring for the time it took to handle the request.
type MonitoredCallback struct {
	apiMonitor *APIMonitor // Reference to the monitor for metrics
	w          http.ResponseWriter
	r          *http.Request
	pattern    string
	t          time.Time
}

func (m *APIMonitor) Callback(w http.ResponseWriter, r *http.Request, pattern string) MonitoredCallback {
	return MonitoredCallback{w: w, r: r, pattern: pattern, t: time.Now()}
}

// Respond to the request with the given code and error.
// Also log the time it took to handle the request.
func (h MonitoredCallback) Respond(code int, err error, text string) {
	if h.apiMonitor != nil {
		observer := h.apiMonitor.ApiRequestsTimer.WithLabelValues(
			h.r.Method,
			h.pattern,
			strconv.Itoa(code),
			text, // Internal error messages should not face the monitor.
		)
		observer.Observe(time.Since(h.t).Seconds())
	}
	if err != nil {
		slog.Error("failed to handle request", "error", err)
		http.Error(h.w, text, code)
		return
	}
	// If there was no error, nothing else to do.
}
