// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package failover

import (
	"github.com/prometheus/client_golang/prometheus"
)

var azLabel = []string{"availability_zone"}

// FailoverMonitor provides Prometheus metrics for the failover reconciliation controller.
type FailoverMonitor struct {
	reconciliationRuns     *prometheus.CounterVec
	reconciliationDuration *prometheus.HistogramVec
	totalVMs               *prometheus.GaugeVec
	totalReservations      *prometheus.GaugeVec
	vmsMissingFailover     *prometheus.GaugeVec
	vmsProcessed           *prometheus.CounterVec
	reservationsNeeded     *prometheus.CounterVec
	reservationsReused     *prometheus.CounterVec
	reservationsCreated    *prometheus.CounterVec
	reservationsFailed     *prometheus.CounterVec
	reservationsUpdated    *prometheus.CounterVec
	reservationsDeleted    *prometheus.CounterVec
}

// NewFailoverMonitor creates a new monitor with Prometheus metrics.
func NewFailoverMonitor() *FailoverMonitor {
	m := &FailoverMonitor{
		reconciliationRuns: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "cortex_failover_reconciliation_runs_total",
			Help: "Total number of failover periodic reconciliation runs since pod restart",
		}, azLabel),
		reconciliationDuration: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name:    "cortex_failover_reconciliation_duration_seconds",
			Help:    "Duration of failover periodic reconciliation cycles",
			Buckets: []float64{0.1, 0.25, 0.5, 1, 2.5, 5, 10, 30, 60},
		}, azLabel),
		totalVMs: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name: "cortex_failover_reconciliation_total_vms",
			Help: "Total number of VMs seen during the last reconciliation",
		}, azLabel),
		totalReservations: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name: "cortex_failover_reconciliation_total_reservations",
			Help: "Total number of failover reservations during the last reconciliation",
		}, azLabel),
		vmsMissingFailover: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name: "cortex_failover_reconciliation_vms_missing_failover",
			Help: "Number of VMs missing required failover reservations during the last reconciliation",
		}, azLabel),
		vmsProcessed: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "cortex_failover_reconciliation_vms_processed_total",
			Help: "Total number of VMs processed across all reconciliation cycles since pod restart",
		}, azLabel),
		reservationsNeeded: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "cortex_failover_reconciliation_reservations_needed_total",
			Help: "Total number of reservations needed across all reconciliation cycles since pod restart",
		}, azLabel),
		reservationsReused: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "cortex_failover_reconciliation_reservations_reused_total",
			Help: "Total number of reservations reused across all reconciliation cycles since pod restart",
		}, azLabel),
		reservationsCreated: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "cortex_failover_reconciliation_reservations_created_total",
			Help: "Total number of reservations created across all reconciliation cycles since pod restart",
		}, azLabel),
		reservationsFailed: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "cortex_failover_reconciliation_reservations_failed_total",
			Help: "Total number of failed reservation attempts across all reconciliation cycles since pod restart",
		}, azLabel),
		reservationsUpdated: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "cortex_failover_reconciliation_reservations_updated_total",
			Help: "Total number of reservation allocation updates across all reconciliation cycles since pod restart",
		}, azLabel),
		reservationsDeleted: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "cortex_failover_reconciliation_reservations_deleted_total",
			Help: "Total number of empty reservations deleted across all reconciliation cycles since pod restart",
		}, azLabel),
	}

	// Pre-initialize the aggregate label so metrics appear even before the first reconciliation.
	m.preInitialize("")

	return m
}

func (m *FailoverMonitor) preInitialize(az string) {
	m.reconciliationRuns.WithLabelValues(az)
	m.reconciliationDuration.WithLabelValues(az)
	m.totalVMs.WithLabelValues(az)
	m.totalReservations.WithLabelValues(az)
	m.vmsMissingFailover.WithLabelValues(az)
	m.vmsProcessed.WithLabelValues(az)
	m.reservationsNeeded.WithLabelValues(az)
	m.reservationsReused.WithLabelValues(az)
	m.reservationsCreated.WithLabelValues(az)
	m.reservationsFailed.WithLabelValues(az)
	m.reservationsUpdated.WithLabelValues(az)
	m.reservationsDeleted.WithLabelValues(az)
}

// RecordReconciliation records all metrics from a single reconciliation cycle.
// The availabilityZone parameter allows future per-AZ reporting; pass "" for aggregate.
func (m *FailoverMonitor) RecordReconciliation(summary reconcileSummary, availabilityZone string) {
	m.reconciliationRuns.WithLabelValues(availabilityZone).Inc()
	m.reconciliationDuration.WithLabelValues(availabilityZone).Observe(summary.duration.Seconds())
	m.totalVMs.WithLabelValues(availabilityZone).Set(float64(summary.totalVMs))
	m.totalReservations.WithLabelValues(availabilityZone).Set(float64(summary.totalReservations))
	m.vmsMissingFailover.WithLabelValues(availabilityZone).Set(float64(summary.vmsMissingFailover))
	m.vmsProcessed.WithLabelValues(availabilityZone).Add(float64(summary.vmsProcessed))
	m.reservationsNeeded.WithLabelValues(availabilityZone).Add(float64(summary.reservationsNeeded))
	m.reservationsReused.WithLabelValues(availabilityZone).Add(float64(summary.totalReused))
	m.reservationsCreated.WithLabelValues(availabilityZone).Add(float64(summary.totalCreated))
	m.reservationsFailed.WithLabelValues(availabilityZone).Add(float64(summary.totalFailed))
	m.reservationsUpdated.WithLabelValues(availabilityZone).Add(float64(summary.reservationsUpdated))
	m.reservationsDeleted.WithLabelValues(availabilityZone).Add(float64(summary.reservationsDeleted))
}

// Describe implements prometheus.Collector.
func (m *FailoverMonitor) Describe(ch chan<- *prometheus.Desc) {
	m.reconciliationRuns.Describe(ch)
	m.reconciliationDuration.Describe(ch)
	m.totalVMs.Describe(ch)
	m.totalReservations.Describe(ch)
	m.vmsMissingFailover.Describe(ch)
	m.vmsProcessed.Describe(ch)
	m.reservationsNeeded.Describe(ch)
	m.reservationsReused.Describe(ch)
	m.reservationsCreated.Describe(ch)
	m.reservationsFailed.Describe(ch)
	m.reservationsUpdated.Describe(ch)
	m.reservationsDeleted.Describe(ch)
}

// Collect implements prometheus.Collector.
func (m *FailoverMonitor) Collect(ch chan<- prometheus.Metric) {
	m.reconciliationRuns.Collect(ch)
	m.reconciliationDuration.Collect(ch)
	m.totalVMs.Collect(ch)
	m.totalReservations.Collect(ch)
	m.vmsMissingFailover.Collect(ch)
	m.vmsProcessed.Collect(ch)
	m.reservationsNeeded.Collect(ch)
	m.reservationsReused.Collect(ch)
	m.reservationsCreated.Collect(ch)
	m.reservationsFailed.Collect(ch)
	m.reservationsUpdated.Collect(ch)
	m.reservationsDeleted.Collect(ch)
}
