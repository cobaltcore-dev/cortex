// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package api

import (
	"github.com/prometheus/client_golang/prometheus"
)

// ChangeCommitmentsAPIMonitor provides metrics for the CR change API.
type ChangeCommitmentsAPIMonitor struct {
	requestCounter    *prometheus.CounterVec
	requestDuration   *prometheus.HistogramVec
	commitmentChanges *prometheus.CounterVec
	timeouts          prometheus.Counter
}

// NewChangeCommitmentsAPIMonitor creates a new monitor with Prometheus metrics.
// Metrics are pre-initialized with zero values for common HTTP status codes
// to ensure they appear in Prometheus before the first request.
func NewChangeCommitmentsAPIMonitor() ChangeCommitmentsAPIMonitor {
	m := ChangeCommitmentsAPIMonitor{
		requestCounter: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "cortex_committed_resource_change_api_requests_total",
			Help: "Total number of committed resource change API requests by HTTP status code",
		}, []string{"status_code"}),
		requestDuration: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name: "cortex_committed_resource_change_api_request_duration_seconds",
			Help: "Duration of committed resource change API requests in seconds by HTTP status code",
		}, []string{"status_code"}),
		commitmentChanges: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "cortex_committed_resource_change_api_commitment_changes_total",
			Help: "Total number of commitment changes processed by result",
		}, []string{"result"}),
		timeouts: prometheus.NewCounter(prometheus.CounterOpts{
			Name: "cortex_committed_resource_change_api_timeouts_total",
			Help: "Total number of commitment change requests that timed out while waiting for reservations to become ready",
		}),
	}

	// Pre-initialize metrics with zero values for common HTTP status codes.
	// This ensures metrics exist in Prometheus before the first request,
	// preventing "metric missing" warnings in alerting rules.
	for _, statusCode := range []string{"200", "400", "409", "500", "503"} {
		m.requestCounter.WithLabelValues(statusCode)
		m.requestDuration.WithLabelValues(statusCode)
	}

	// Pre-initialize commitment change result labels
	for _, result := range []string{"accepted", "rejected"} {
		m.commitmentChanges.WithLabelValues(result)
	}

	return m
}

// Describe implements prometheus.Collector.
func (m *ChangeCommitmentsAPIMonitor) Describe(ch chan<- *prometheus.Desc) {
	m.requestCounter.Describe(ch)
	m.requestDuration.Describe(ch)
	m.commitmentChanges.Describe(ch)
	m.timeouts.Describe(ch)
}

// Collect implements prometheus.Collector.
func (m *ChangeCommitmentsAPIMonitor) Collect(ch chan<- prometheus.Metric) {
	m.requestCounter.Collect(ch)
	m.requestDuration.Collect(ch)
	m.commitmentChanges.Collect(ch)
	m.timeouts.Collect(ch)
}
