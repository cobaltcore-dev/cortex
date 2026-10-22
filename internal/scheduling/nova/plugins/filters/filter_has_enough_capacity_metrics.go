// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package filters

import "github.com/prometheus/client_golang/prometheus"

// HostCapacityMetrics reports per-host overload state observed during capacity
// probe runs. A host is overloaded for a resource when its VM allocation exceeds
// its effective capacity (freeCPU/freeMem < 0).
//
// Updated only on CapacityProbeIntent requests (not empty-datacenter probes) so
// the gauge reflects real allocation pressure, not synthetic empty-datacenter
// checks.
type HostCapacityMetrics struct {
	// overloaded is 1 when allocation > effective capacity for the host+resource
	// pair, 0 otherwise.
	overloaded *prometheus.GaugeVec
}

// NewHostCapacityMetrics creates a new HostCapacityMetrics. Register the result
// with the Prometheus registry (metrics.Registry.MustRegister) before use.
func NewHostCapacityMetrics() *HostCapacityMetrics {
	return &HostCapacityMetrics{
		overloaded: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name: "cortex_scheduler_host_overloaded",
			Help: "Excess units by which the host's VM allocation exceeds its effective capacity " +
				"for the given resource (cpu: cores, memory: bytes). " +
				"0 means healthy; positive means overloaded by that amount. " +
				"Observed during capacity probe runs; stale between probes.",
		}, []string{"host", "resource"}),
	}
}

// SetOverloaded records the excess for host+resource. Pass 0 for a healthy host.
func (m *HostCapacityMetrics) SetOverloaded(host, resource string, excess float64) {
	if m == nil || m.overloaded == nil {
		return
	}
	m.overloaded.WithLabelValues(host, resource).Set(excess)
}

// Describe implements prometheus.Collector.
func (m *HostCapacityMetrics) Describe(ch chan<- *prometheus.Desc) {
	if m == nil || m.overloaded == nil {
		return
	}
	m.overloaded.Describe(ch)
}

// Collect implements prometheus.Collector.
func (m *HostCapacityMetrics) Collect(ch chan<- prometheus.Metric) {
	if m == nil || m.overloaded == nil {
		return
	}
	m.overloaded.Collect(ch)
}

// HostCapacityMetricsSingleton is initialized from cmd/manager/main.go.
// SetOverloaded is nil-safe, so filters that run without a wired singleton
// (e.g. unit tests) are unaffected.
var HostCapacityMetricsSingleton *HostCapacityMetrics
