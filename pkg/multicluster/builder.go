// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package multicluster

import (
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	"sigs.k8s.io/controller-runtime/pkg/source"
)

// Build a multicluster controller using the multicluster client.
// Use this builder to watch resources across multiple clusters.
func BuildController(c *Client, mgr manager.Manager) MulticlusterBuilder {
	return MulticlusterBuilder{
		Builder:            ctrl.NewControllerManagedBy(mgr),
		multiclusterClient: c,
	}
}

// Builder which provides special methods to watch resources across multiple clusters.
type MulticlusterBuilder struct {
	// Wrapped builder provided by controller-runtime.
	*builder.Builder
	// The multicluster client to use for watching resources.
	multiclusterClient *Client
}

// WatchesMulticluster watches a resource across all clusters that serve its GVK.
// If the GVK is served by multiple remote clusters, a watch is set up on each.
func (b MulticlusterBuilder) WatchesMulticluster(object client.Object, eventHandler handler.TypedEventHandler[client.Object, reconcile.Request], predicates ...predicate.Predicate) MulticlusterBuilder {
	gvk, err := b.multiclusterClient.GVKFromHomeScheme(object)
	if err != nil {
		// Fall back to home cluster if GVK lookup fails.
		clusterCache := b.multiclusterClient.HomeCluster.GetCache()
		b.Builder = b.WatchesRawSource(source.Kind(clusterCache, object, eventHandler, predicates...))
		return b
	}

	// Add a watch for each remote cluster serving the GVK
	for _, cl := range b.multiclusterClient.ClustersForGVK(gvk) {
		clusterCache := cl.GetCache()
		b.Builder = b.WatchesRawSource(source.Kind(clusterCache, object, eventHandler, predicates...))
	}
	return b
}
