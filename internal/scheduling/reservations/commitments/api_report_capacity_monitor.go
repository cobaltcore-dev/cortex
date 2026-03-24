// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package commitments

import (
	"github.com/prometheus/client_golang/prometheus"
)

// ReportCapacityAPIMonitor provides metrics for the CR report-capacity API.
type ReportCapacityAPIMonitor struct {
	requestCounter  *prometheus.CounterVec
	requestDuration *prometheus.HistogramVec
}

// NewReportCapacityAPIMonitor creates a new monitor with Prometheus metrics.
func NewReportCapacityAPIMonitor() ReportCapacityAPIMonitor {
	return ReportCapacityAPIMonitor{
		requestCounter: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "cortex_committed_resource_capacity_api_requests_total",
			Help: "Total number of committed resource capacity API requests by HTTP status code",
		}, []string{"status_code"}),
		requestDuration: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name:    "cortex_committed_resource_capacity_api_request_duration_seconds",
			Help:    "Duration of committed resource capacity API requests in seconds by HTTP status code",
			Buckets: []float64{0.1, 0.25, 0.5, 1, 2.5, 5, 10},
		}, []string{"status_code"}),
	}
}

// Describe implements prometheus.Collector.
func (m *ReportCapacityAPIMonitor) Describe(ch chan<- *prometheus.Desc) {
	m.requestCounter.Describe(ch)
	m.requestDuration.Describe(ch)
}

// Collect implements prometheus.Collector.
func (m *ReportCapacityAPIMonitor) Collect(ch chan<- prometheus.Metric) {
	m.requestCounter.Collect(ch)
	m.requestDuration.Collect(ch)
}
