// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package commitments

import (
	"github.com/prometheus/client_golang/prometheus"
)

// SyncerMonitor provides metrics for the commitment syncer.
type SyncerMonitor struct {
	syncRuns     prometheus.Counter
	syncErrors   prometheus.Counter
	unitMismatch *prometheus.CounterVec
}

// NewSyncerMonitor creates a new monitor with Prometheus metrics.
func NewSyncerMonitor() *SyncerMonitor {
	m := &SyncerMonitor{
		syncRuns: prometheus.NewCounter(prometheus.CounterOpts{
			Name: "cortex_committed_resource_syncer_runs_total",
			Help: "Total number of commitment syncer runs",
		}),
		syncErrors: prometheus.NewCounter(prometheus.CounterOpts{
			Name: "cortex_committed_resource_syncer_errors_total",
			Help: "Total number of commitment syncer errors",
		}),
		unitMismatch: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "cortex_committed_resource_syncer_unit_mismatch_total",
			Help: "Total number of commitments with unit mismatch between Limes and Cortex flavor group knowledge",
		}, []string{"flavor_group"}),
	}
	return m
}

// RecordUnitMismatch records a unit mismatch for a flavor group.
func (m *SyncerMonitor) RecordUnitMismatch(flavorGroup string) {
	m.unitMismatch.WithLabelValues(flavorGroup).Inc()
}

// RecordSyncRun records a syncer run.
func (m *SyncerMonitor) RecordSyncRun() {
	m.syncRuns.Inc()
}

// RecordSyncError records a syncer error.
func (m *SyncerMonitor) RecordSyncError() {
	m.syncErrors.Inc()
}

// Describe implements prometheus.Collector.
func (m *SyncerMonitor) Describe(ch chan<- *prometheus.Desc) {
	m.syncRuns.Describe(ch)
	m.syncErrors.Describe(ch)
	m.unitMismatch.Describe(ch)
}

// Collect implements prometheus.Collector.
func (m *SyncerMonitor) Collect(ch chan<- prometheus.Metric) {
	m.syncRuns.Collect(ch)
	m.syncErrors.Collect(ch)
	m.unitMismatch.Collect(ch)
}
