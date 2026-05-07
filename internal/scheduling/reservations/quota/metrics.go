// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package quota

import (
	"github.com/prometheus/client_golang/prometheus"
)

// QuotaMetrics holds Prometheus metrics for the quota controller.
type QuotaMetrics struct {
	totalUsageGauge    *prometheus.GaugeVec
	paygUsageGauge     *prometheus.GaugeVec
	crUsageGauge       *prometheus.GaugeVec
	reconcileDuration  prometheus.Histogram
	reconcileResultVec *prometheus.CounterVec
}

// NewQuotaMetrics creates a new QuotaMetrics instance and registers with the given registerer.
func NewQuotaMetrics(reg prometheus.Registerer) *QuotaMetrics {
	m := &QuotaMetrics{
		totalUsageGauge: prometheus.NewGaugeVec(
			prometheus.GaugeOpts{
				Name: "cortex_quota_total_usage",
				Help: "Total resource usage per project/AZ/resource",
			},
			[]string{"project_id", "availability_zone", "resource"},
		),
		paygUsageGauge: prometheus.NewGaugeVec(
			prometheus.GaugeOpts{
				Name: "cortex_quota_payg_usage",
				Help: "Pay-as-you-go usage per project/AZ/resource",
			},
			[]string{"project_id", "availability_zone", "resource"},
		),
		crUsageGauge: prometheus.NewGaugeVec(
			prometheus.GaugeOpts{
				Name: "cortex_quota_cr_usage",
				Help: "Committed resource usage per project/AZ/resource",
			},
			[]string{"project_id", "availability_zone", "resource"},
		),
		reconcileDuration: prometheus.NewHistogram(
			prometheus.HistogramOpts{
				Name:    "cortex_quota_reconcile_duration_seconds",
				Help:    "Duration of quota controller full reconcile",
				Buckets: prometheus.ExponentialBuckets(0.1, 2, 10),
			},
		),
		reconcileResultVec: prometheus.NewCounterVec(
			prometheus.CounterOpts{
				Name: "cortex_quota_reconcile_total",
				Help: "Total number of periodic reconcile attempts by result (success/failure)",
			},
			[]string{"result"},
		),
	}

	if reg != nil {
		reg.MustRegister(m.totalUsageGauge)
		reg.MustRegister(m.paygUsageGauge)
		reg.MustRegister(m.crUsageGauge)
		reg.MustRegister(m.reconcileDuration)
		reg.MustRegister(m.reconcileResultVec)
	}

	return m
}

// RecordUsage records usage metrics for a project/AZ/resource.
func (m *QuotaMetrics) RecordUsage(projectID, az, resource string, totalUsage, paygUsage, crUsage int64) {
	if m == nil {
		return
	}
	m.totalUsageGauge.WithLabelValues(projectID, az, resource).Set(float64(totalUsage))
	m.paygUsageGauge.WithLabelValues(projectID, az, resource).Set(float64(paygUsage))
	m.crUsageGauge.WithLabelValues(projectID, az, resource).Set(float64(crUsage))
}

// RecordReconcileDuration records the duration of a full reconcile.
func (m *QuotaMetrics) RecordReconcileDuration(seconds float64) {
	if m == nil {
		return
	}
	m.reconcileDuration.Observe(seconds)
}

// RecordReconcileResult increments the success or failure counter for periodic reconciles.
func (m *QuotaMetrics) RecordReconcileResult(success bool) {
	if m == nil {
		return
	}
	result := "failure"
	if success {
		result = "success"
	}
	m.reconcileResultVec.WithLabelValues(result).Inc()
}
