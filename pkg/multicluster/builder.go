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

// Watch resources across multiple clusters.
//
// If the object implements Resource, we pick the right cluster based on the
// resource URI. If your builder needs this method, pass it to the builder
// as the first call and then proceed with other builder methods.
func (b MulticlusterBuilder) WatchesMulticluster(object client.Object, eventHandler handler.TypedEventHandler[client.Object, reconcile.Request], predicates ...predicate.Predicate) MulticlusterBuilder {
	cl := b.multiclusterClient.HomeCluster // default cluster
	if gvk, err := b.multiclusterClient.GVKFromHomeScheme(object); err == nil {
		cl = b.multiclusterClient.ClusterForResource(gvk)
	}
	clusterCache := cl.GetCache()
	b.Builder = b.WatchesRawSource(source.Kind(clusterCache, object, eventHandler, predicates...))
	return b
}
