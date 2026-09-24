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

	// recordRemoteReachable records whether a remote apiserver was reachable at
	// the last reachability probe, labeled by host. See pkg/multicluster's
	// ProbeRemotes for how reachability is determined.
	recordRemoteReachable(host string, reachable bool)
}

// monitor is the default Prometheus-backed Monitor implementation.
type monitor struct {
	// crossClusterNameConflicts counts how often the same namespace/name was
	// detected on more than one cluster serving the GVK, labeled by the method
	// of access and the resource GVK.
	crossClusterNameConflicts *prometheus.CounterVec
	// remoteReachable reports whether each remote apiserver was reachable at the
	// last reachability probe, labeled by host. It lives on the process-lifetime
	// Monitor (not the per-cycle Client) so it survives manager rebuilds.
	remoteReachable *prometheus.GaugeVec
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
		remoteReachable: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name: prefix + "multicluster_remote_apiserver_reachable",
			Help: "1 if the remote apiserver was reachable at the last probe, 0 if sustained-unreachable, labeled by host",
		}, []string{"host"}),
	}
}

// recordCrossClusterNameConflict increments the conflict counter for the given
// access method and GVK.
func (m *monitor) recordCrossClusterNameConflict(method string, gvk schema.GroupVersionKind) {
	m.crossClusterNameConflicts.WithLabelValues(method, gvk.String()).Inc()
}

// recordRemoteReachable sets the reachability gauge for the given host to 1
// (reachable) or 0 (unreachable).
func (m *monitor) recordRemoteReachable(host string, reachable bool) {
	v := 0.0
	if reachable {
		v = 1.0
	}
	m.remoteReachable.WithLabelValues(host).Set(v)
}

// Describe implements prometheus.Collector.
func (m *monitor) Describe(ch chan<- *prometheus.Desc) {
	m.crossClusterNameConflicts.Describe(ch)
	m.remoteReachable.Describe(ch)
}

// Collect implements prometheus.Collector.
func (m *monitor) Collect(ch chan<- prometheus.Metric) {
	m.crossClusterNameConflicts.Collect(ch)
	m.remoteReachable.Collect(ch)
}
