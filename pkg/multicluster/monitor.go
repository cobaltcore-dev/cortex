// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package multicluster

import (
	"github.com/prometheus/client_golang/prometheus"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

// duplicateConflictLabels labels the cross-cluster name conflict counter by the
// method of access (e.g. "create", "get", "list") and the resource GVK.
var duplicateConflictLabels = []string{"method", "gvk"}

// Monitor provides Prometheus metrics for the multicluster client. It is
// optional: a nil *Monitor is safe to call and records nothing, so the client
// can be constructed without one in tests or callers that don't wire metrics.
type Monitor struct {
	// crossClusterNameConflicts counts how often the same namespace/name was
	// detected on more than one cluster serving the GVK, labeled by the method
	// of access and the resource GVK.
	crossClusterNameConflicts *prometheus.CounterVec
}

// NewMonitor creates a new multicluster client monitor with Prometheus metrics.
func NewMonitor() *Monitor {
	return &Monitor{
		crossClusterNameConflicts: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "cortex_multicluster_cross_cluster_name_conflicts_total",
			Help: "Total number of times the same resource name was detected on more than one cluster serving the same GVK",
		}, duplicateConflictLabels),
	}
}

// recordCrossClusterNameConflict increments the conflict counter for the given
// access method and GVK. Safe to call on a nil *Monitor.
func (m *Monitor) recordCrossClusterNameConflict(method string, gvk schema.GroupVersionKind) {
	if m == nil {
		return
	}
	m.crossClusterNameConflicts.WithLabelValues(method, gvk.String()).Inc()
}

// Describe implements prometheus.Collector.
func (m *Monitor) Describe(ch chan<- *prometheus.Desc) {
	m.crossClusterNameConflicts.Describe(ch)
}

// Collect implements prometheus.Collector.
func (m *Monitor) Collect(ch chan<- prometheus.Metric) {
	m.crossClusterNameConflicts.Collect(ch)
}
