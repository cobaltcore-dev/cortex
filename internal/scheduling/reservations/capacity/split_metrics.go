// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package capacity

import (
	"github.com/prometheus/client_golang/prometheus"
)

// splitMetricsLabels are the labels shared by the round-robin split metrics.
// "groups" is a comma-separated, sorted list of the flavor groups that
// participated in that AZ's split.
var splitMetricsLabels = []string{"az", "resource", "groups"}

// splitOverlapLabels drop the per-resource dimension: overlap is a host-count,
// not a resource quantity.
var splitOverlapLabels = []string{"az", "groups"}

// SplitMetrics holds the Prometheus metrics derived from the capacity
// controller's round-robin split. Unlike the Monitor collector (which reads
// FlavorGroupCapacity CRDs on scrape), these are per-AZ, cross-group values
// that only exist transiently during a reconcile, so they are pushed
// imperatively from the reconciler.
type SplitMetrics struct {
	stranded        *prometheus.GaugeVec
	overlappingHost *prometheus.GaugeVec
}

// NewSplitMetrics builds the split metrics and registers them with reg.
// A nil reg skips registration (useful in tests that only assert values).
func NewSplitMetrics(reg prometheus.Registerer) *SplitMetrics {
	m := &SplitMetrics{
		stranded: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name: "cortex_committed_resource_capacity_distribution_stranded",
			Help: "Resources on candidate hosts that the round-robin split could not " +
				"attribute to any flavor group (structurally unclaimable fragmentation), " +
				"per AZ and resource. Memory is reported in bytes, cores as a count.",
		}, splitMetricsLabels),
		overlappingHost: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name: "cortex_committed_resource_capacity_overlapping_hosts",
			Help: "Number of hypervisors that are scheduling candidates for more than one " +
				"flavor group in this AZ's split. A non-zero value means those groups " +
				"contend for the same hosts.",
		}, splitOverlapLabels),
	}
	if reg != nil {
		reg.MustRegister(m.stranded)
		reg.MustRegister(m.overlappingHost)
	}
	return m
}

// Reset clears all series. It is called once per reconcile cycle before the
// per-AZ values are re-recorded, so series for AZs or group sets that no longer
// participate in a split do not linger.
func (m *SplitMetrics) Reset() {
	if m == nil {
		return
	}
	m.stranded.Reset()
	m.overlappingHost.Reset()
}

// RecordStranded sets the stranded-capacity gauge for one AZ. It emits a series
// for every resource in unassigned, including zero values, so a healthy "nothing
// stranded" state is observable and trendable.
func (m *SplitMetrics) RecordStranded(az, groups string, unassigned map[string]int64) {
	if m == nil {
		return
	}
	for _, resource := range []string{ResourceMemory, ResourceCores} {
		m.stranded.WithLabelValues(az, resource, groups).Set(float64(unassigned[resource]))
	}
}

// RecordOverlap sets the overlapping-hosts gauge for one AZ.
func (m *SplitMetrics) RecordOverlap(az, groups string, sharedHostCount int) {
	if m == nil {
		return
	}
	m.overlappingHost.WithLabelValues(az, groups).Set(float64(sharedHostCount))
}
