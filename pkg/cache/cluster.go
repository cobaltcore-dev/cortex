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
// structural and checked at the call site (e.g. in main.go).
type Wrapper struct{ conf Config }

// NewWrapper returns a Wrapper that applies the overlay cache to any cluster
// passed to WrapCluster.
func NewWrapper(conf Config) *Wrapper { return &Wrapper{conf} }

// WrapCluster applies the overlay cache to cl, registers the overlay's lifecycle
// Runnable with mgr, and returns the wrapped cluster.
// Satisfies multicluster.ClusterWrapper structurally.
func (w *Wrapper) WrapCluster(mgr manager.Manager, cl cluster.Cluster) (cluster.Cluster, error) {
	wrapped, cleanUpRunnable, err := WrapCluster(cl, w.conf)
	if err != nil {
		return nil, err
	}
	if err := mgr.Add(cleanUpRunnable); err != nil {
		return nil, err
	}
	return wrapped, nil
}
