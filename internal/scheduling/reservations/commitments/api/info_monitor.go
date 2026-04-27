// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package api

import (
	"github.com/prometheus/client_golang/prometheus"
)

// InfoAPIMonitor provides metrics for the CR info API.
type InfoAPIMonitor struct {
	requestCounter  *prometheus.CounterVec
	requestDuration *prometheus.HistogramVec
}

// NewInfoAPIMonitor creates a new monitor with Prometheus metrics.
// Metrics are pre-initialized with zero values for common HTTP status codes
// to ensure they appear in Prometheus before the first request.
func NewInfoAPIMonitor() InfoAPIMonitor {
	m := InfoAPIMonitor{
		requestCounter: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "cortex_committed_resource_info_api_requests_total",
			Help: "Total number of committed resource info API requests by HTTP status code",
		}, []string{"status_code"}),
		requestDuration: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name:    "cortex_committed_resource_info_api_request_duration_seconds",
			Help:    "Duration of committed resource info API requests in seconds by HTTP status code",
			Buckets: []float64{0.1, 0.25, 0.5, 1, 2.5, 5, 10},
		}, []string{"status_code"}),
	}

	// Pre-initialize metrics with zero values for common HTTP status codes.
	// This ensures metrics exist in Prometheus before the first request,
	// preventing "metric missing" warnings in alerting rules.
	for _, statusCode := range []string{"200", "405", "500", "503"} {
		m.requestCounter.WithLabelValues(statusCode)
		m.requestDuration.WithLabelValues(statusCode)
	}

	return m
}

// Describe implements prometheus.Collector.
func (m *InfoAPIMonitor) Describe(ch chan<- *prometheus.Desc) {
	m.requestCounter.Describe(ch)
	m.requestDuration.Describe(ch)
}

// Collect implements prometheus.Collector.
func (m *InfoAPIMonitor) Collect(ch chan<- prometheus.Metric) {
	m.requestCounter.Collect(ch)
	m.requestDuration.Collect(ch)
}
