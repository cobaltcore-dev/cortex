// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package datasources

import (
	"github.com/prometheus/client_golang/prometheus"
)

// Monitor is a collection of Prometheus metrics for the sync package.
type Monitor struct {
	// A gauge to observe the number of objects synced.
	ObjectsGauge *prometheus.GaugeVec
	// A histogram to measure how long each sync request takes.
	RequestTimer *prometheus.HistogramVec
	// A counter to observe the number of processed sync requests.
	RequestProcessedCounter *prometheus.CounterVec
}

// NewSyncMonitor creates a new sync monitor and registers the necessary Prometheus metrics.
func NewSyncMonitor() Monitor {
	return Monitor{
		ObjectsGauge: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name: "cortex_sync_objects",
			Help: "Number of objects synced",
		}, []string{"datasource"}),
		RequestTimer: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name:    "cortex_sync_request_duration_seconds",
			Help:    "Duration of sync request",
			Buckets: prometheus.DefBuckets,
		}, []string{"datasource"}),
		RequestProcessedCounter: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "cortex_sync_request_processed_total",
			Help: "Number of processed sync requests",
		}, []string{"datasource"}),
	}
}

func (m *Monitor) Describe(ch chan<- *prometheus.Desc) {
	m.ObjectsGauge.Describe(ch)
	m.RequestTimer.Describe(ch)
	m.RequestProcessedCounter.Describe(ch)
}

func (m *Monitor) Collect(ch chan<- prometheus.Metric) {
	m.ObjectsGauge.Collect(ch)
	m.RequestTimer.Collect(ch)
	m.RequestProcessedCounter.Collect(ch)
}
