// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package failoverhost

import (
	"time"

	"github.com/prometheus/client_golang/prometheus"
)

// Monitor provides Prometheus metrics for the failover host controller. It
// covers two activities: the periodic calculation loop that recomputes the
// desired reservations, and the reconciler that drives the actual Reservation
// objects toward that desired state.
//
// Unlike the sibling failover controller, the calculation loop here is
// cluster-wide (one pass covers every partition and availability zone at once,
// and a partition clique is not guaranteed to be single-AZ), so the loop-level
// metrics carry no availability-zone label. The scheduler-request and reconcile
// metrics do carry a small label each where it is actionable.
type Monitor struct {
	calculationRuns      prometheus.Counter
	calculationDuration  prometheus.Histogram
	calculationAborts    *prometheus.CounterVec
	partitions           prometheus.Gauge
	desiredReservations  prometheus.Gauge
	existingReservations prometheus.Gauge

	schedulerRequests        *prometheus.CounterVec
	schedulerRequestDuration prometheus.Histogram

	reservationsCreated prometheus.Counter
	reservationsUpdated prometheus.Counter
	reservationsDeleted prometheus.Counter
	reservationsErrors  *prometheus.CounterVec
}

// calculationSummary holds the statistics of a single calculation-loop pass,
// filled across the pass and recorded once at the end via RecordCalculation.
type calculationSummary struct {
	duration             time.Duration
	partitions           int
	desiredReservations  int
	existingReservations int
}

// NewMonitor creates a new monitor with Prometheus metrics.
func NewMonitor() *Monitor {
	m := &Monitor{
		calculationRuns: prometheus.NewCounter(prometheus.CounterOpts{
			Name: "cortex_failover_host_calculation_runs_total",
			Help: "Total number of completed failover host calculation-loop passes since pod restart",
		}),
		calculationDuration: prometheus.NewHistogram(prometheus.HistogramOpts{
			Name:    "cortex_failover_host_calculation_duration_seconds",
			Help:    "Duration of failover host calculation-loop passes",
			Buckets: []float64{0.1, 0.25, 0.5, 1, 2.5, 5, 10, 30, 60},
		}),
		calculationAborts: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "cortex_failover_host_calculation_aborts_total",
			Help: "Total number of failover host calculation-loop passes aborted before producing a desired state, by reason",
		}, []string{"reason"}),
		partitions: prometheus.NewGauge(prometheus.GaugeOpts{
			Name: "cortex_failover_host_partitions",
			Help: "Number of failover partitions from the last calculation-loop pass",
		}),
		desiredReservations: prometheus.NewGauge(prometheus.GaugeOpts{
			Name: "cortex_failover_host_desired_reservations",
			Help: "Number of desired failover host reservations from the last calculation-loop pass",
		}),
		existingReservations: prometheus.NewGauge(prometheus.GaugeOpts{
			Name: "cortex_failover_host_existing_reservations",
			Help: "Number of failover host reservations present in the cluster at the last calculation-loop pass",
		}),
		schedulerRequests: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "cortex_failover_host_scheduler_requests_total",
			Help: "Total number of external scheduler requests issued by the failover host path finder, by status (HTTP status code or 'error' on transport failure)",
		}, []string{"status"}),
		schedulerRequestDuration: prometheus.NewHistogram(prometheus.HistogramOpts{
			Name:    "cortex_failover_host_scheduler_request_duration_seconds",
			Help:    "Duration of external scheduler requests issued by the failover host path finder",
			Buckets: []float64{0.025, 0.05, 0.1, 0.25, 0.5, 1, 2.5, 5, 10, 30},
		}),
		reservationsCreated: prometheus.NewCounter(prometheus.CounterOpts{
			Name: "cortex_failover_host_reservations_created_total",
			Help: "Total number of failover host reservations created since pod restart",
		}),
		reservationsUpdated: prometheus.NewCounter(prometheus.CounterOpts{
			Name: "cortex_failover_host_reservations_updated_total",
			Help: "Total number of failover host reservations updated since pod restart",
		}),
		reservationsDeleted: prometheus.NewCounter(prometheus.CounterOpts{
			Name: "cortex_failover_host_reservations_deleted_total",
			Help: "Total number of stale failover host reservations deleted since pod restart",
		}),
		reservationsErrors: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "cortex_failover_host_reservations_errors_total",
			Help: "Total number of failed failover host reservation writes since pod restart, by operation",
		}, []string{"op"}),
	}
	// Pre-initialize the labelled series so they appear at zero before the first
	// event, which lets rate()/increase() alerts behave from pod startup.
	m.calculationAborts.WithLabelValues("collect")
	m.calculationAborts.WithLabelValues("seeding")
	m.calculationAborts.WithLabelValues("pathfinding")
	m.reservationsErrors.WithLabelValues("create")
	m.reservationsErrors.WithLabelValues("update")
	m.reservationsErrors.WithLabelValues("delete")
	return m
}

// RecordCalculation records the metrics of a single completed calculation pass.
func (m *Monitor) RecordCalculation(summary calculationSummary) {
	if m == nil {
		return
	}
	m.calculationRuns.Inc()
	m.calculationDuration.Observe(summary.duration.Seconds())
	m.partitions.Set(float64(summary.partitions))
	m.desiredReservations.Set(float64(summary.desiredReservations))
	m.existingReservations.Set(float64(summary.existingReservations))
}

// RecordCalculationAbort records a calculation pass that aborted before
// producing a desired state. reason is "collect", "seeding" or "pathfinding".
func (m *Monitor) RecordCalculationAbort(reason string) {
	if m == nil {
		return
	}
	m.calculationAborts.WithLabelValues(reason).Inc()
}

// RecordSchedulerRequest records one external scheduler request. status is the
// HTTP status code as a string, or "error" on a transport-level failure.
func (m *Monitor) RecordSchedulerRequest(status string, duration time.Duration) {
	if m == nil {
		return
	}
	m.schedulerRequests.WithLabelValues(status).Inc()
	m.schedulerRequestDuration.Observe(duration.Seconds())
}

// RecordReservationCreated records a successful reservation create.
func (m *Monitor) RecordReservationCreated() {
	if m == nil {
		return
	}
	m.reservationsCreated.Inc()
}

// RecordReservationUpdated records a successful reservation update.
func (m *Monitor) RecordReservationUpdated() {
	if m == nil {
		return
	}
	m.reservationsUpdated.Inc()
}

// RecordReservationDeleted records a successful stale-reservation delete.
func (m *Monitor) RecordReservationDeleted() {
	if m == nil {
		return
	}
	m.reservationsDeleted.Inc()
}

// RecordReservationError records a failed reservation write. op is "create",
// "update" or "delete".
func (m *Monitor) RecordReservationError(op string) {
	if m == nil {
		return
	}
	m.reservationsErrors.WithLabelValues(op).Inc()
}

// Describe implements prometheus.Collector.
func (m *Monitor) Describe(ch chan<- *prometheus.Desc) {
	m.calculationRuns.Describe(ch)
	m.calculationDuration.Describe(ch)
	m.calculationAborts.Describe(ch)
	m.partitions.Describe(ch)
	m.desiredReservations.Describe(ch)
	m.existingReservations.Describe(ch)
	m.schedulerRequests.Describe(ch)
	m.schedulerRequestDuration.Describe(ch)
	m.reservationsCreated.Describe(ch)
	m.reservationsUpdated.Describe(ch)
	m.reservationsDeleted.Describe(ch)
	m.reservationsErrors.Describe(ch)
}

// Collect implements prometheus.Collector.
func (m *Monitor) Collect(ch chan<- prometheus.Metric) {
	m.calculationRuns.Collect(ch)
	m.calculationDuration.Collect(ch)
	m.calculationAborts.Collect(ch)
	m.partitions.Collect(ch)
	m.desiredReservations.Collect(ch)
	m.existingReservations.Collect(ch)
	m.schedulerRequests.Collect(ch)
	m.schedulerRequestDuration.Collect(ch)
	m.reservationsCreated.Collect(ch)
	m.reservationsUpdated.Collect(ch)
	m.reservationsDeleted.Collect(ch)
	m.reservationsErrors.Collect(ch)
}
