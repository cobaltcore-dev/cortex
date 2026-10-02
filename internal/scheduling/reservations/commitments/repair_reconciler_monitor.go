// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package commitments

import "github.com/prometheus/client_golang/prometheus"

// RepairReconcilerMonitor provides Prometheus metrics for the repair reconciler.
type RepairReconcilerMonitor struct {
	// slotsFixedTotal counts reservation slots modified per repair run (stale/duplicate
	// removed or filled), labelled by flavor group and availability zone.
	slotsFixedTotal *prometheus.CounterVec
	// slotFillRatio is a histogram of the ratio of assigned-VM memory to committed slot
	// memory at the end of each repair run (scheduling vs billing perspective).
	// A value of 1.0 means all committed capacity has a VM assigned; 0.0 means none.
	slotFillRatio prometheus.Histogram
}

// NewRepairReconcilerMonitor creates a new monitor and registers its Prometheus collectors.
func NewRepairReconcilerMonitor() RepairReconcilerMonitor {
	return RepairReconcilerMonitor{
		slotsFixedTotal: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "cortex_committed_resource_repair_slots_fixed_total",
			Help: "Number of reservation slots modified by the repair reconciler (stale or duplicate entries removed, or slots filled with VM assignments).",
		}, []string{"flavor_group", "availability_zone"}),
		slotFillRatio: prometheus.NewHistogram(prometheus.HistogramOpts{
			Name:    "cortex_committed_resource_repair_slot_fill_ratio",
			Help:    "Ratio of assigned VM memory to committed slot memory at the end of a repair run (scheduling vs billing perspective). 1.0 means all committed capacity has a matching VM assignment.",
			Buckets: []float64{0.1, 0.2, 0.3, 0.4, 0.5, 0.6, 0.7, 0.8, 0.9, 1.0},
		}),
	}
}

// Describe implements prometheus.Collector.
func (m RepairReconcilerMonitor) Describe(ch chan<- *prometheus.Desc) {
	m.slotsFixedTotal.Describe(ch)
	m.slotFillRatio.Describe(ch)
}

// Collect implements prometheus.Collector.
func (m RepairReconcilerMonitor) Collect(ch chan<- prometheus.Metric) {
	m.slotsFixedTotal.Collect(ch)
	m.slotFillRatio.Collect(ch)
}
