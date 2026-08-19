// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package pendingcache

import (
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/cluster"
)

// overlayCluster wraps a cluster.Cluster so its client and field indexer are
// served through the in-process overlay cache. Everything else (config, scheme,
// REST mapper, HTTP client, API reader, event recorder, and Start) is delegated
// to the embedded inner cluster unchanged.
//
// It deliberately does NOT override Start: the manager already owns the inner
// cluster's lifecycle (home cluster) or drives it via mgr.Add (remotes). The
// overlay's own lifecycle (eviction handlers + TTL cleanup) is a separate
// manager.Runnable — the *Overlay returned alongside this wrapper by WrapCluster.
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
