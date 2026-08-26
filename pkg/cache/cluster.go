// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package cache

import (
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/cluster"
	"sigs.k8s.io/controller-runtime/pkg/manager"
)

// overlayCluster wraps a cluster.Cluster so its client and field indexer are
// served through the in-process overlay cache. Everything else (config, scheme,
// REST mapper, HTTP client, API reader, event recorder, and Start) is delegated
// to the embedded inner cluster unchanged.
//
// It deliberately does NOT override Start: the manager already owns the inner
// cluster's lifecycle (home cluster) or drives it via mgr.Add (remotes). The
// overlay's own lifecycle (eviction handlers + TTL cleanup) is registered with
// the manager directly by Wrapper.WrapCluster.
type overlayCluster struct {
	cluster.Cluster
	cache *Overlay
}

// GetClient returns the overlay client so per-cluster reads and writes flow
// through the cache.
func (w *overlayCluster) GetClient() client.Client { return w.cache }

// GetFieldIndexer returns the overlay so IndexField dual-registers the indexer
// with both the real cache (via Overlay.IndexField) and the overlay.
func (w *overlayCluster) GetFieldIndexer() client.FieldIndexer { return w.cache }

// Wrapper implements the multicluster.ClusterWrapper interface for the overlay
// cache. It does not import the multicluster package; interface satisfaction is
// structural and checked at the call site (e.g. in main.go). It captures the
// manager at construction time because the ClusterWrapper interface does not
// pass a manager to WrapCluster.
type Wrapper struct {
	mgr     manager.Manager
	conf    Config
	monitor *Monitor
}

// NewWrapper returns a Wrapper that applies the overlay cache to any cluster
// passed to WrapCluster, registering the overlay's lifecycle Runnable with mgr.
// The monitor is shared across every wrapped cluster and is supplied by the
// caller (so this library package hardcodes no project-specific metric prefix);
// it may be nil to disable overlay size metrics. Register it on the metrics
// registry from the caller.
func NewWrapper(mgr manager.Manager, conf Config, monitor *Monitor) *Wrapper {
	return &Wrapper{
		mgr:     mgr,
		conf:    conf,
		monitor: monitor,
	}
}

// WrapCluster applies the overlay cache to cl, registers the overlay's lifecycle
// Runnable with the captured manager, and returns the wrapped cluster.
// Satisfies multicluster.ClusterWrapper structurally.
func (w *Wrapper) WrapCluster(cl cluster.Cluster) (cluster.Cluster, error) {
	wrapped, cleanUpRunnable, err := WrapCluster(cl, w.conf, w.monitor)
	if err != nil {
		return nil, err
	}
	if err := w.mgr.Add(cleanUpRunnable); err != nil {
		return nil, err
	}
	return wrapped, nil
}
