// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package commitments

import (
	"github.com/prometheus/client_golang/prometheus"
)

// Skip reason labels for commitment processing
const (
	SkipReasonUnitMismatch       = "unit_mismatch"
	SkipReasonUnknownFlavorGroup = "unknown_flavor_group"
	SkipReasonInvalidResource    = "invalid_resource_name"
	SkipReasonEmptyUUID          = "empty_uuid"
	SkipReasonNonCompute         = "non_compute"
)

// SyncerMonitor provides metrics for the commitment syncer.
type SyncerMonitor struct {
	syncErrors             prometheus.Counter
	syncDuration           prometheus.Histogram
	limesCommitmentsActive prometheus.Gauge
	staleCRs               prometheus.Gauge
	commitmentsSkipped     *prometheus.CounterVec
	crCreates              prometheus.Counter
	crUpdates              prometheus.Counter
	crDeletes              prometheus.Counter
}

// NewSyncerMonitor creates a new monitor with Prometheus metrics.
func NewSyncerMonitor() *SyncerMonitor {
	m := &SyncerMonitor{
		syncErrors: prometheus.NewCounter(prometheus.CounterOpts{
			Name: "cortex_committed_resource_syncer_errors_total",
			Help: "Total number of commitment syncer runs that failed",
		}),
		syncDuration: prometheus.NewHistogram(prometheus.HistogramOpts{
			Name:    "cortex_committed_resource_syncer_duration_seconds",
			Help:    "Duration of each commitment syncer run",
			Buckets: []float64{0.5, 1, 5, 10, 30, 60, 120},
		}),
		limesCommitmentsActive: prometheus.NewGauge(prometheus.GaugeOpts{
			Name: "cortex_committed_resource_syncer_limes_commitments_active",
			Help: "Number of commitments from Limes that passed filtering and should have CR CRDs",
		}),
		staleCRs: prometheus.NewGauge(prometheus.GaugeOpts{
			Name: "cortex_committed_resource_syncer_crd_unmatched",
			Help: "Number of CommittedResource CRDs present locally but absent from Limes",
		}),
		commitmentsSkipped: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "cortex_committed_resource_syncer_commitments_skipped_total",
			Help: "Total number of commitments skipped during sync",
		}, []string{"reason"}),
		crCreates: prometheus.NewCounter(prometheus.CounterOpts{
			Name: "cortex_committed_resource_syncer_cr_creates_total",
			Help: "Total number of CommittedResource CRDs created by the syncer",
		}),
		crUpdates: prometheus.NewCounter(prometheus.CounterOpts{
			Name: "cortex_committed_resource_syncer_cr_updates_total",
			Help: "Total number of CommittedResource CRDs updated by the syncer",
		}),
		crDeletes: prometheus.NewCounter(prometheus.CounterOpts{
			Name: "cortex_committed_resource_syncer_cr_deletes_total",
			Help: "Total number of CommittedResource CRDs deleted by the syncer (expired GC)",
		}),
	}

	// Pre-initialize skip reason labels
	for _, reason := range []string{
		SkipReasonUnitMismatch,
		SkipReasonUnknownFlavorGroup,
		SkipReasonInvalidResource,
		SkipReasonEmptyUUID,
		SkipReasonNonCompute,
	} {
		m.commitmentsSkipped.WithLabelValues(reason)
	}

	return m
}

func (m *SyncerMonitor) RecordError() {
	m.syncErrors.Inc()
}

func (m *SyncerMonitor) RecordDuration(seconds float64) {
	m.syncDuration.Observe(seconds)
}

func (m *SyncerMonitor) SetLimesCommitmentsActive(count int) {
	m.limesCommitmentsActive.Set(float64(count))
}

func (m *SyncerMonitor) RecordStaleCRs(count int) {
	m.staleCRs.Set(float64(count))
}

func (m *SyncerMonitor) RecordCommitmentSkipped(reason string) {
	m.commitmentsSkipped.WithLabelValues(reason).Inc()
}

func (m *SyncerMonitor) RecordCRCreates(count int) {
	m.crCreates.Add(float64(count))
}

func (m *SyncerMonitor) RecordCRUpdates(count int) {
	m.crUpdates.Add(float64(count))
}

func (m *SyncerMonitor) RecordCRDeletes(count int) {
	m.crDeletes.Add(float64(count))
}

// Describe implements prometheus.Collector.
func (m *SyncerMonitor) Describe(ch chan<- *prometheus.Desc) {
	m.syncErrors.Describe(ch)
	m.syncDuration.Describe(ch)
	m.limesCommitmentsActive.Describe(ch)
	m.staleCRs.Describe(ch)
	m.commitmentsSkipped.Describe(ch)
	m.crCreates.Describe(ch)
	m.crUpdates.Describe(ch)
	m.crDeletes.Describe(ch)
}

// Collect implements prometheus.Collector.
func (m *SyncerMonitor) Collect(ch chan<- prometheus.Metric) {
	m.syncErrors.Collect(ch)
	m.syncDuration.Collect(ch)
	m.limesCommitmentsActive.Collect(ch)
	m.staleCRs.Collect(ch)
	m.commitmentsSkipped.Collect(ch)
	m.crCreates.Collect(ch)
	m.crUpdates.Collect(ch)
	m.crDeletes.Collect(ch)
}
