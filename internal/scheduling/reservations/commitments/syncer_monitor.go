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
	SkipReasonNonActive          = "non_active"
)

// SyncerMonitor provides metrics for the commitment syncer.
type SyncerMonitor struct {
	// Sync lifecycle
	syncRuns   prometheus.Counter
	syncErrors prometheus.Counter

	// Commitment processing
	commitmentsTotal     prometheus.Counter     // all commitments seen from Limes
	commitmentsProcessed prometheus.Counter     // successfully processed
	commitmentsSkipped   *prometheus.CounterVec // skipped with reason label

	// Reservation changes
	reservationsCreated  prometheus.Counter
	reservationsDeleted  prometheus.Counter
	reservationsRepaired prometheus.Counter
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
		commitmentsTotal: prometheus.NewCounter(prometheus.CounterOpts{
			Name: "cortex_committed_resource_syncer_commitments_total",
			Help: "Total number of commitments seen from Limes",
		}),
		commitmentsProcessed: prometheus.NewCounter(prometheus.CounterOpts{
			Name: "cortex_committed_resource_syncer_commitments_processed_total",
			Help: "Total number of commitments successfully processed",
		}),
		commitmentsSkipped: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "cortex_committed_resource_syncer_commitments_skipped_total",
			Help: "Total number of commitments skipped during sync",
		}, []string{"reason"}),
		reservationsCreated: prometheus.NewCounter(prometheus.CounterOpts{
			Name: "cortex_committed_resource_syncer_reservations_created_total",
			Help: "Total number of reservations created during sync",
		}),
		reservationsDeleted: prometheus.NewCounter(prometheus.CounterOpts{
			Name: "cortex_committed_resource_syncer_reservations_deleted_total",
			Help: "Total number of reservations deleted during sync",
		}),
		reservationsRepaired: prometheus.NewCounter(prometheus.CounterOpts{
			Name: "cortex_committed_resource_syncer_reservations_repaired_total",
			Help: "Total number of reservations repaired during sync (wrong metadata)",
		}),
	}

	// Pre-initialize skip reason labels
	for _, reason := range []string{
		SkipReasonUnitMismatch,
		SkipReasonUnknownFlavorGroup,
		SkipReasonInvalidResource,
		SkipReasonEmptyUUID,
		SkipReasonNonCompute,
		SkipReasonNonActive,
	} {
		m.commitmentsSkipped.WithLabelValues(reason)
	}

	return m
}

// RecordSyncRun records a syncer run.
func (m *SyncerMonitor) RecordSyncRun() {
	m.syncRuns.Inc()
}

// RecordSyncError records a syncer error.
func (m *SyncerMonitor) RecordSyncError() {
	m.syncErrors.Inc()
}

// RecordCommitmentSeen records a commitment seen from Limes.
func (m *SyncerMonitor) RecordCommitmentSeen() {
	m.commitmentsTotal.Inc()
}

// RecordCommitmentProcessed records a commitment successfully processed.
func (m *SyncerMonitor) RecordCommitmentProcessed() {
	m.commitmentsProcessed.Inc()
}

// RecordCommitmentSkipped records a commitment skipped with a reason.
func (m *SyncerMonitor) RecordCommitmentSkipped(reason string) {
	m.commitmentsSkipped.WithLabelValues(reason).Inc()
}

// RecordReservationsCreated records reservations created.
func (m *SyncerMonitor) RecordReservationsCreated(count int) {
	m.reservationsCreated.Add(float64(count))
}

// RecordReservationsDeleted records reservations deleted.
func (m *SyncerMonitor) RecordReservationsDeleted(count int) {
	m.reservationsDeleted.Add(float64(count))
}

// RecordReservationsRepaired records reservations repaired.
func (m *SyncerMonitor) RecordReservationsRepaired(count int) {
	m.reservationsRepaired.Add(float64(count))
}

// Describe implements prometheus.Collector.
func (m *SyncerMonitor) Describe(ch chan<- *prometheus.Desc) {
	m.syncRuns.Describe(ch)
	m.syncErrors.Describe(ch)
	m.commitmentsTotal.Describe(ch)
	m.commitmentsProcessed.Describe(ch)
	m.commitmentsSkipped.Describe(ch)
	m.reservationsCreated.Describe(ch)
	m.reservationsDeleted.Describe(ch)
	m.reservationsRepaired.Describe(ch)
}

// Collect implements prometheus.Collector.
func (m *SyncerMonitor) Collect(ch chan<- prometheus.Metric) {
	m.syncRuns.Collect(ch)
	m.syncErrors.Collect(ch)
	m.commitmentsTotal.Collect(ch)
	m.commitmentsProcessed.Collect(ch)
	m.commitmentsSkipped.Collect(ch)
	m.reservationsCreated.Collect(ch)
	m.reservationsDeleted.Collect(ch)
	m.reservationsRepaired.Collect(ch)
}
