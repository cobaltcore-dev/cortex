// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package failoverhost

import (
	"github.com/prometheus/client_golang/prometheus"
)

// MatcherMonitor is the small Prometheus collector owned by the scheduling
// pipeline's failover host Matcher. It is kept separate from the failover host
// controller's Monitor so both can be registered independently: the pipeline
// controller and the failover-host-reservations-controller can be enabled at
// once without their collectors colliding on shared metric names.
type MatcherMonitor struct {
	evacuationsWithoutReservation *prometheus.CounterVec
}

// NewMatcherMonitor creates a new matcher monitor with Prometheus metrics.
func NewMatcherMonitor() *MatcherMonitor {
	return &MatcherMonitor{
		evacuationsWithoutReservation: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "cortex_failover_host_evacuations_without_reservation_total",
			Help: "Total number of evacuation scheduling requests for which no failover host reservation matched the source host, by availability zone",
		}, []string{"az"}),
	}
}

// RecordEvacuationWithoutReservation records an evacuation scheduling request
// whose source host matched no failover host reservation, so the request fell
// back to the ordinary candidate set. az is the request's availability zone
// (may be empty if unknown).
func (m *MatcherMonitor) RecordEvacuationWithoutReservation(az string) {
	if m == nil {
		return
	}
	m.evacuationsWithoutReservation.WithLabelValues(az).Inc()
}

// Describe implements prometheus.Collector.
func (m *MatcherMonitor) Describe(ch chan<- *prometheus.Desc) {
	m.evacuationsWithoutReservation.Describe(ch)
}

// Collect implements prometheus.Collector.
func (m *MatcherMonitor) Collect(ch chan<- prometheus.Metric) {
	m.evacuationsWithoutReservation.Collect(ch)
}
