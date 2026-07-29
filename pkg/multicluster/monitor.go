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

// Monitor is the metrics sink for the multicluster client. It is optional on
// the Client: a nil Monitor causes recording to be skipped entirely. It embeds
// prometheus.Collector so a concrete implementation can be registered with a
// Prometheus registry.
type Monitor interface {
	prometheus.Collector

	// recordCrossClusterNameConflict is called when the same namespace/name was
	// detected on more than one cluster serving the GVK, labeled by the method
	// of access and the resource GVK.
	recordCrossClusterNameConflict(method string, gvk schema.GroupVersionKind)
}

// monitor is the default Prometheus-backed Monitor implementation.
type monitor struct {
	// crossClusterNameConflicts counts how often the same namespace/name was
	// detected on more than one cluster serving the GVK, labeled by the method
	// of access and the resource GVK.
	crossClusterNameConflicts *prometheus.CounterVec
}

// NewMonitor creates a new Prometheus-backed multicluster client monitor. The
// prefix is prepended to every metric name (e.g. pass "cortex_" to produce
// "cortex_multicluster_cross_cluster_name_conflicts_total").
func NewMonitor(prefix string) Monitor {
	return &monitor{
		crossClusterNameConflicts: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: prefix + "multicluster_cross_cluster_name_conflicts_total",
			Help: "Total number of times the same resource name was detected on more than one cluster serving the same GVK",
		}, duplicateConflictLabels),
	}
}

// recordCrossClusterNameConflict increments the conflict counter for the given
// access method and GVK.
func (m *monitor) recordCrossClusterNameConflict(method string, gvk schema.GroupVersionKind) {
	m.crossClusterNameConflicts.WithLabelValues(method, gvk.String()).Inc()
}

// Describe implements prometheus.Collector.
func (m *monitor) Describe(ch chan<- *prometheus.Desc) {
	m.crossClusterNameConflicts.Describe(ch)
}

// Collect implements prometheus.Collector.
func (m *monitor) Collect(ch chan<- prometheus.Metric) {
	m.crossClusterNameConflicts.Collect(ch)
}
