// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package inflight

import (
	"github.com/cobaltcore-dev/cortex/api/v1alpha1"
	"github.com/prometheus/client_golang/prometheus"
)

// Monitor holds Prometheus metrics for the in-flight reservation controller.
type Monitor struct {
	// ttlExpiriesVec counts in-flight reservations released by the optional TTL
	// backstop because they carried an EndTime that passed before the owning
	// operation signalled completion, labeled by scheduling intent.
	ttlExpiriesVec *prometheus.CounterVec
	// anomaliesVec counts reconcile anomalies worth alerting on, labeled by a
	// stable reason (e.g. no_owner_handler, ambiguous_owner). Alert on any
	// increase, then inspect the controller logs for details.
	anomaliesVec *prometheus.CounterVec
}

// Anomaly reasons for the anomaly counter.
const (
	AnomalyNoOwnerHandler = "no_owner_handler"
	AnomalyAmbiguousOwner = "ambiguous_owner"
)

// NewMonitor creates a Monitor and registers its metrics with the given
// registerer. A nil registerer skips registration (useful in tests).
func NewMonitor(reg prometheus.Registerer) *Monitor {
	m := &Monitor{
		ttlExpiriesVec: prometheus.NewCounterVec(
			prometheus.CounterOpts{
				Name: "cortex_inflight_reservation_ttl_expired_total",
				Help: "Total number of in-flight reservations released by the TTL backstop after their EndTime passed",
			},
			[]string{"intent"},
		),
		anomaliesVec: prometheus.NewCounterVec(
			prometheus.CounterOpts{
				Name: "cortex_inflight_reservation_anomaly_total",
				Help: "Total number of in-flight reservation reconcile anomalies, by reason",
			},
			[]string{"reason"},
		),
	}
	if reg != nil {
		reg.MustRegister(m.ttlExpiriesVec, m.anomaliesVec)
	}
	return m
}

// RecordTTLExpiry increments the TTL-expiry counter for the given intent. Safe
// to call on a nil Monitor.
func (m *Monitor) RecordTTLExpiry(intent v1alpha1.SchedulingIntent) {
	if m == nil {
		return
	}
	m.ttlExpiriesVec.WithLabelValues(string(intent)).Inc()
}

// RecordAnomaly increments the anomaly counter for the given reason. Safe to
// call on a nil Monitor.
func (m *Monitor) RecordAnomaly(reason string) {
	if m == nil {
		return
	}
	m.anomaliesVec.WithLabelValues(reason).Inc()
}
