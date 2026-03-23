// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package commitments

import (
	"github.com/prometheus/client_golang/prometheus"
)

// ChangeCommitmentsAPIMonitor provides metrics for the CR change API.
type ChangeCommitmentsAPIMonitor struct {
	requestCounter    *prometheus.CounterVec
	requestDuration   *prometheus.HistogramVec
	commitmentChanges *prometheus.CounterVec
}

// NewChangeCommitmentsAPIMonitor creates a new monitor with Prometheus metrics.
func NewChangeCommitmentsAPIMonitor() ChangeCommitmentsAPIMonitor {
	return ChangeCommitmentsAPIMonitor{
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
	}
}

// Describe implements prometheus.Collector.
func (m *ChangeCommitmentsAPIMonitor) Describe(ch chan<- *prometheus.Desc) {
	m.requestCounter.Describe(ch)
	m.requestDuration.Describe(ch)
	m.commitmentChanges.Describe(ch)
}

// Collect implements prometheus.Collector.
func (m *ChangeCommitmentsAPIMonitor) Collect(ch chan<- prometheus.Metric) {
	m.requestCounter.Collect(ch)
	m.requestDuration.Collect(ch)
	m.commitmentChanges.Collect(ch)
}
