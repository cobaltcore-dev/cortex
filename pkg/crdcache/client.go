// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package crdcache

import (
	"context"
	"reflect"

	apimeta "k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// CachingClient wraps a client.Client and transparently populates/queries a CRDCache
// for write and read operations. Callers use a normal client.Client interface — no
// explicit cache API is exposed.
type CachingClient struct {
	client.Client
	cache *CRDCache
	opts  Options
}

// NewCachingClient returns a CachingClient wrapping inner with the given cache.
func NewCachingClient(inner client.Client, cache *CRDCache, opts Options) *CachingClient {
	return &CachingClient{Client: inner, cache: cache, opts: opts}
}

func (c *CachingClient) shouldCache(obj client.Object) bool {
	if c.opts.ShouldCache == nil {
		return true
	}
	return c.opts.ShouldCache(obj)
}

// Create delegates to the inner client and, on success, assumes the object in the cache.
func (c *CachingClient) Create(ctx context.Context, obj client.Object, opts ...client.CreateOption) error {
	if err := c.Client.Create(ctx, obj, opts...); err != nil {
		return err
	}
	if c.shouldCache(obj) {
		c.cache.Assume(obj)
	}
	return nil
}

// Update delegates to the inner client and, on success, updates the cache entry.
func (c *CachingClient) Update(ctx context.Context, obj client.Object, opts ...client.UpdateOption) error {
	if err := c.Client.Update(ctx, obj, opts...); err != nil {
		return err
	}
	if c.shouldCache(obj) {
		c.cache.Assume(obj)
	}
	return nil
}

// Patch delegates to the inner client and, on success, updates the cache entry.
func (c *CachingClient) Patch(ctx context.Context, obj client.Object, patch client.Patch, opts ...client.PatchOption) error {
	if err := c.Client.Patch(ctx, obj, patch, opts...); err != nil {
		return err
	}
	if c.shouldCache(obj) {
		c.cache.Assume(obj)
	}
	return nil
}

// Delete delegates to the inner client and, on success, removes the entry from cache.
func (c *CachingClient) Delete(ctx context.Context, obj client.Object, opts ...client.DeleteOption) error {
	if err := c.Client.Delete(ctx, obj, opts...); err != nil {
		return err
	}
	c.cache.Forget(ObjectKey(obj))
	return nil
}

// Get checks the cache first; on miss it delegates to the inner client.
func (c *CachingClient) Get(ctx context.Context, key client.ObjectKey, obj client.Object, opts ...client.GetOption) error {
	cacheKey := key.Namespace + "/" + key.Name
	if cached, ok := c.cache.Get(cacheKey); ok {
		reflect.ValueOf(obj).Elem().Set(reflect.ValueOf(cached).Elem())
		return nil
	}
	return c.Client.Get(ctx, key, obj, opts...)
}

// List calls the inner client and overlays cached entries not yet visible in the informer result.
// Objects already present in the informer result are not replaced — informer truth takes precedence.
// Objects present in the cache but absent from the informer are appended to the result.
func (c *CachingClient) List(ctx context.Context, list client.ObjectList, opts ...client.ListOption) error {
	if err := c.Client.List(ctx, list, opts...); err != nil {
		return err
	}

	// Build a set of keys already in the informer result.
	items, err := apimeta.ExtractList(list)
	if err != nil {
		return err
	}
	seen := make(map[string]struct{}, len(items))
	for _, item := range items {
		if o, ok := item.(client.Object); ok {
			seen[ObjectKey(o)] = struct{}{}
		}
	}

	// Parse list options once for filtering cached entries.
	listOpts := &client.ListOptions{}
	for _, o := range opts {
		o.ApplyToList(listOpts)
	}

	// Append cached entries that are not yet in the informer result.
	for _, cached := range c.cache.List() {
		if _, present := seen[ObjectKey(cached)]; present {
			continue
		}
		if !matchesListOpts(cached, listOpts) {
			continue
		}
		items = append(items, cached)
	}

	return apimeta.SetList(list, items)
}

// matchesListOpts returns true if obj satisfies the namespace and label selector constraints
// in opts. Field selectors are not evaluated against cached entries (safe to include them).
func matchesListOpts(obj client.Object, opts *client.ListOptions) bool {
	if opts.Namespace != "" && obj.GetNamespace() != opts.Namespace {
		return false
	}
	if opts.LabelSelector != nil && !opts.LabelSelector.Matches(labels.Set(obj.GetLabels())) {
		return false
	}
	return true
}

// apimeta.SetList requires []runtime.Object.
var _ runtime.Object = (client.Object)(nil)
