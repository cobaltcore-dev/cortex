// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package commitments

import (
	"github.com/prometheus/client_golang/prometheus"
)

// ReservationControllerMonitor reports per-host over-subscription violations
// detected by the CommitmentReservationController.
type ReservationControllerMonitor struct {
	oversubscribed *prometheus.GaugeVec
	slotsUnplaced  *prometheus.CounterVec
}

func NewReservationControllerMonitor() ReservationControllerMonitor {
	return ReservationControllerMonitor{
		oversubscribed: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name: "cortex_committed_resource_host_oversubscribed",
			Help: "Excess resource units by which a host's reservation blocks + VM allocations exceed its effective capacity. " +
				"Non-zero when the host is over-subscribed and unresolvable via unallocated slot eviction.",
		}, []string{"host", "az", "resource"}),
		slotsUnplaced: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "cortex_committed_resource_host_oversubscribed_unplaced_reservations_total",
			Help: "Total number of reservation slots evicted by the oversubscription controller. " +
				"A sustained increase indicates recurring oversubscription events in an AZ.",
		}, []string{"az"}),
	}
}

// SetOversubscribed records the excess amount for a host+resource pair.
// Zero clears the violation.
func (m *ReservationControllerMonitor) SetOversubscribed(host, az, resource string, excessUnits float64) {
	m.oversubscribed.WithLabelValues(host, az, resource).Set(excessUnits)
}

// ClearHost resets all resource gauges for a host that is no longer over-subscribed.
func (m *ReservationControllerMonitor) ClearHost(host, az string) {
	m.oversubscribed.DeletePartialMatch(prometheus.Labels{"host": host, "az": az})
}

// IncSlotsUnplaced increments the counter for slots evicted in the given AZ.
func (m *ReservationControllerMonitor) IncSlotsUnplaced(az string) {
	m.slotsUnplaced.WithLabelValues(az).Inc()
}

// Describe implements prometheus.Collector.
func (m *ReservationControllerMonitor) Describe(ch chan<- *prometheus.Desc) {
	m.oversubscribed.Describe(ch)
	m.slotsUnplaced.Describe(ch)
}

// Collect implements prometheus.Collector.
func (m *ReservationControllerMonitor) Collect(ch chan<- prometheus.Metric) {
	m.oversubscribed.Collect(ch)
	m.slotsUnplaced.Collect(ch)
}
