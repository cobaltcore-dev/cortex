// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package clientcache

import (
	"context"
	"fmt"
	"time"

	"k8s.io/apimachinery/pkg/runtime/schema"
	toolscachek8s "k8s.io/client-go/tools/cache"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// minCleanupInterval bounds the TTL cleanup ticker from below.
const minCleanupInterval = 30 * time.Second

// Start implements manager.Runnable. It attaches informer event handlers for
// eviction and runs the TTL cleanup loop until ctx is done.
func (c *CachingClient) Start(ctx context.Context) error {
	log := ctrl.LoggerFrom(ctx).WithName("clientcache")

	for gvk := range c.gvks {
		obj, err := c.newObjectForGVK(gvk)
		if err != nil {
			log.Error(err, "failed to build object for gvk; eviction disabled for it", "gvk", gvk)
			continue
		}
		informers, err := c.informers.GetInformersForKind(ctx, obj)
		if err != nil {
			log.Error(err, "failed to get informers for gvk; eviction disabled for it", "gvk", gvk)
			continue
		}
		handler := c.evictionHandler(gvk)
		for _, inf := range informers {
			if _, err := inf.AddEventHandler(handler); err != nil {
				log.Error(err, "failed to add eviction event handler", "gvk", gvk)
			}
		}
	}

	interval := max(c.ttl/4, minCleanupInterval)
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case now := <-ticker.C:
			c.cleanupExpired(now)
		}
	}
}

// evictionHandler returns an informer event handler that evicts overlay entries
// for the GVK when the real object is observed at a >= ResourceVersion.
func (c *CachingClient) evictionHandler(gvk schema.GroupVersionKind) toolscachek8s.ResourceEventHandler {
	evict := func(o any) {
		obj, ok := o.(client.Object)
		if !ok {
			return
		}
		c.evictIfSeen(gvk, obj)
	}
	return toolscachek8s.ResourceEventHandlerFuncs{
		AddFunc:    func(o any) { evict(o) },
		UpdateFunc: func(_, o any) { evict(o) },
	}
}

// newObjectForGVK builds an empty typed object for the GVK using the scheme.
func (c *CachingClient) newObjectForGVK(gvk schema.GroupVersionKind) (client.Object, error) {
	ro, err := c.scheme.New(gvk)
	if err != nil {
		return nil, err
	}
	obj, ok := ro.(client.Object)
	if !ok {
		return nil, fmt.Errorf("clientcache: object for gvk %s does not implement client.Object", gvk)
	}
	return obj, nil
}
