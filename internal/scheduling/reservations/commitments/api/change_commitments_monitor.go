// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package api

import (
	"github.com/prometheus/client_golang/prometheus"
)

type ChangeCommitmentsAPIMonitor struct {
	requestCounter    *prometheus.CounterVec
	requestDuration   *prometheus.HistogramVec
	commitmentChanges *prometheus.CounterVec
	timeouts          *prometheus.CounterVec
}

func NewChangeCommitmentsAPIMonitor() ChangeCommitmentsAPIMonitor {
	m := ChangeCommitmentsAPIMonitor{
		requestCounter: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "cortex_committed_resource_change_api_requests_total",
			Help: "Total number of committed resource change API requests by HTTP status code",
		}, []string{"status_code", "dry_run", "result"}),
		requestDuration: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name:    "cortex_committed_resource_change_api_request_duration_seconds",
			Help:    "Duration of committed resource change API requests in seconds by HTTP status code",
			Buckets: []float64{0.5, 1, 2.5, 5, 7.5, 10, 12.5, 15, 20, 30},
		}, []string{"status_code", "dry_run"}),
		commitmentChanges: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "cortex_committed_resource_change_api_commitment_changes_total",
			Help: "Total number of commitment changes processed by result and availability zone",
		}, []string{"result", "az", "dry_run"}),
		timeouts: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "cortex_committed_resource_change_api_timeouts_total",
			Help: "Total number of commitment change requests that timed out while waiting for reservations to become ready",
		}, []string{"dry_run"}),
	}

	// Pre-initialize so metrics exist before the first request, preventing "metric missing" alert warnings.
	// commitmentChanges uses az="" sentinel: az values are dynamic, and sums in alerts aggregate across all az values.
	for _, dryRun := range []string{"true", "false"} {
		for _, statusCode := range []string{"200", "400", "405", "409", "500", "503"} {
			for _, result := range []string{"accepted", "rejected", "error"} {
				m.requestCounter.WithLabelValues(statusCode, dryRun, result)
			}
			m.requestDuration.WithLabelValues(statusCode, dryRun)
		}
		m.timeouts.WithLabelValues(dryRun)
		for _, result := range []string{"accepted", "rejected", "error"} {
			m.commitmentChanges.WithLabelValues(result, "", dryRun)
		}
	}

	return m
}

func (m *ChangeCommitmentsAPIMonitor) Describe(ch chan<- *prometheus.Desc) {
	m.requestCounter.Describe(ch)
	m.requestDuration.Describe(ch)
	m.commitmentChanges.Describe(ch)
	m.timeouts.Describe(ch)
}

func (m *ChangeCommitmentsAPIMonitor) Collect(ch chan<- prometheus.Metric) {
	m.requestCounter.Collect(ch)
	m.requestDuration.Collect(ch)
	m.commitmentChanges.Collect(ch)
	m.timeouts.Collect(ch)
}
