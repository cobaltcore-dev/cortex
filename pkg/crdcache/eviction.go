// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package crdcache

import (
	"context"

	runtimecache "sigs.k8s.io/controller-runtime/pkg/cache"
	"sigs.k8s.io/controller-runtime/pkg/client"
	toolscache "k8s.io/client-go/tools/cache"
)

// RegisterEvictionHandler wires the cache's Forget callback into the given cluster cache's
// informer for the given prototype object type. When the informer observes an Add, Update,
// or Delete for an object that is currently assumed, the cache entry is evicted.
//
// This must be called after the manager has started (informers are running), typically
// inside a manager.RunnableFunc. Call once per cluster that serves the GVK.
func (c *CRDCache) RegisterEvictionHandler(
	ctx context.Context,
	clusterCache runtimecache.Cache,
	prototype client.Object,
) error {
	informer, err := clusterCache.GetInformer(ctx, prototype)
	if err != nil {
		return err
	}
	_, err = informer.AddEventHandler(toolscache.ResourceEventHandlerFuncs{
		AddFunc: func(obj any) {
			if o, ok := obj.(client.Object); ok {
				c.Forget(ObjectKey(o))
			}
		},
		UpdateFunc: func(_, newObj any) {
			if o, ok := newObj.(client.Object); ok {
				c.Forget(ObjectKey(o))
			}
		},
		DeleteFunc: func(obj any) {
			if o, ok := obj.(client.Object); ok {
				c.Forget(ObjectKey(o))
			}
		},
	})
	return err
}
