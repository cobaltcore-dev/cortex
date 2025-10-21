// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package datasources

import (
	"github.com/prometheus/client_golang/prometheus"
)

// Monitor is a collection of Prometheus metrics for the sync package.
type Monitor struct {
	// A histogram to measure how long each sync run takes.
	PipelineRunTimer *prometheus.HistogramVec
	// A gauge to observe the number of objects synced.
	PipelineObjectsGauge *prometheus.GaugeVec
	// A histogram to measure how long each sync request takes.
	PipelineRequestTimer *prometheus.HistogramVec
	// A counter to observe the number of processed sync requests.
	PipelineRequestProcessedCounter *prometheus.CounterVec
}

// NewSyncMonitor creates a new sync monitor and registers the necessary Prometheus metrics.
func NewSyncMonitor() Monitor {
	pipelineRunTimer := prometheus.NewHistogramVec(prometheus.HistogramOpts{
		Name:    "cortex_sync_run_duration_seconds",
		Help:    "Duration of sync run",
		Buckets: prometheus.ExponentialBuckets(0.001, 2, 21), // 0.001s to ~1048s in 21 buckets,
	}, []string{"datasource"})
	pipelineObjectsGauge := prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "cortex_sync_objects",
		Help: "Number of objects synced",
	}, []string{"datasource"})
	pipelineRequestTimer := prometheus.NewHistogramVec(prometheus.HistogramOpts{
		Name:    "cortex_sync_request_duration_seconds",
		Help:    "Duration of sync request",
		Buckets: prometheus.DefBuckets,
	}, []string{"datasource"})
	pipelineRequestProcessedCounter := prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "cortex_sync_request_processed_total",
		Help: "Number of processed sync requests",
	}, []string{"datasource"})
	return Monitor{
		PipelineRunTimer:                pipelineRunTimer,
		PipelineObjectsGauge:            pipelineObjectsGauge,
		PipelineRequestTimer:            pipelineRequestTimer,
		PipelineRequestProcessedCounter: pipelineRequestProcessedCounter,
	}
}

func (m *Monitor) Describe(ch chan<- *prometheus.Desc) {
	m.PipelineRunTimer.Describe(ch)
	m.PipelineObjectsGauge.Describe(ch)
	m.PipelineRequestTimer.Describe(ch)
	m.PipelineRequestProcessedCounter.Describe(ch)
}

func (m *Monitor) Collect(ch chan<- prometheus.Metric) {
	m.PipelineRunTimer.Collect(ch)
	m.PipelineObjectsGauge.Collect(ch)
	m.PipelineRequestTimer.Collect(ch)
	m.PipelineRequestProcessedCounter.Collect(ch)
}
