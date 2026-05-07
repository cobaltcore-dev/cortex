// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package commitments

import (
	"github.com/prometheus/client_golang/prometheus"
)

// UsageReconcilerMonitor provides metrics for the usage reconciler.
type UsageReconcilerMonitor struct {
	reconcileDuration *prometheus.HistogramVec
	statusAge         prometheus.Histogram
	assignedInstances *prometheus.GaugeVec
}

// NewUsageReconcilerMonitor creates a new monitor with Prometheus metrics.
func NewUsageReconcilerMonitor() UsageReconcilerMonitor {
	m := UsageReconcilerMonitor{
		reconcileDuration: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name:    "cortex_cr_usage_reconcile_duration_seconds",
			Help:    "Duration of committed resource usage reconcile runs in seconds.",
			Buckets: []float64{0.1, 0.25, 0.5, 1, 2.5, 5, 10, 30},
		}, []string{"result"}),
		statusAge: prometheus.NewHistogram(prometheus.HistogramOpts{
			Name:    "cortex_cr_usage_status_age_seconds",
			Help:    "Age of CommittedResource usage status at reconcile time, in seconds. Distribution across all active commitments shows freshness spread.",
			Buckets: []float64{30, 60, 120, 300, 600, 900, 1800},
		}),
		assignedInstances: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name: "cortex_cr_usage_assigned_vms_total",
			Help: "Number of VMs currently assigned to committed resources for a project.",
		}, []string{"project_id"}),
	}

	// Pre-initialize result labels so metrics appear before the first reconcile.
	m.reconcileDuration.WithLabelValues("success")
	m.reconcileDuration.WithLabelValues("error")

	return m
}

// Describe implements prometheus.Collector.
func (m UsageReconcilerMonitor) Describe(ch chan<- *prometheus.Desc) {
	m.reconcileDuration.Describe(ch)
	m.statusAge.Describe(ch)
	m.assignedInstances.Describe(ch)
}

// Collect implements prometheus.Collector.
func (m UsageReconcilerMonitor) Collect(ch chan<- prometheus.Metric) {
	m.reconcileDuration.Collect(ch)
	m.statusAge.Collect(ch)
	m.assignedInstances.Collect(ch)
}
