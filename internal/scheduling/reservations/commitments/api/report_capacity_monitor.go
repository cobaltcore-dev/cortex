// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package api

import (
	"github.com/prometheus/client_golang/prometheus"
)

// ReportCapacityAPIMonitor provides metrics for the CR report-capacity API.
type ReportCapacityAPIMonitor struct {
	requestCounter   *prometheus.CounterVec
	requestDuration  *prometheus.HistogramVec
	reportedCapacity *prometheus.GaugeVec
}

// NewReportCapacityAPIMonitor creates a new monitor with Prometheus metrics.
// Metrics are pre-initialized with zero values for common HTTP status codes
// to ensure they appear in Prometheus before the first request.
func NewReportCapacityAPIMonitor() ReportCapacityAPIMonitor {
	m := ReportCapacityAPIMonitor{
		requestCounter: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "cortex_committed_resource_capacity_api_requests_total",
			Help: "Total number of committed resource capacity API requests by HTTP status code",
		}, []string{"status_code"}),
		requestDuration: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name:    "cortex_committed_resource_capacity_api_request_duration_seconds",
			Help:    "Duration of committed resource capacity API requests in seconds by HTTP status code",
			Buckets: []float64{0.1, 0.25, 0.5, 1, 2.5, 5, 10},
		}, []string{"status_code"}),
		reportedCapacity: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name: "cortex_committed_resource_reported_capacity_gib",
			Help: "Last reported capacity in GiB per resource and availability zone as returned by the capacity API",
		}, []string{"resource", "az"}),
	}

	// Pre-initialize metrics with zero values for common HTTP status codes.
	// This ensures metrics exist in Prometheus before the first request,
	// preventing "metric missing" warnings in alerting rules.
	for _, statusCode := range []string{"200", "500", "503"} {
		m.requestCounter.WithLabelValues(statusCode)
		m.requestDuration.WithLabelValues(statusCode)
	}

	return m
}

// Describe implements prometheus.Collector.
func (m *ReportCapacityAPIMonitor) Describe(ch chan<- *prometheus.Desc) {
	m.requestCounter.Describe(ch)
	m.requestDuration.Describe(ch)
	m.reportedCapacity.Describe(ch)
}

// Collect implements prometheus.Collector.
func (m *ReportCapacityAPIMonitor) Collect(ch chan<- prometheus.Metric) {
	m.requestCounter.Collect(ch)
	m.requestDuration.Collect(ch)
	m.reportedCapacity.Collect(ch)
}
