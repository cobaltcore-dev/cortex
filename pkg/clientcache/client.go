// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package clientcache

import (
	"context"
	"errors"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// defaultTTL is used when Config.TTL is zero.
const defaultTTL = 2 * time.Minute

// CachingClient wraps an inner client.Client with a transparent in-process
// overlay. Writes populate the overlay; reads merge the overlay with the inner
// (informer-backed) result; entries are evicted once the real object appears in
// an informer (see runnable.go) or after TTL expiry.
//
// It embeds client.Client so all methods not overridden below are delegated to
// the inner client unchanged.
type CachingClient struct {
	client.Client // inner client, used for delegation

	informers InformerSource
	scheme    *runtime.Scheme
	overlay   *overlay
	ttl       time.Duration
	gvks      map[schema.GroupVersionKind]bool
}

// New builds a CachingClient wrapping inner. informers supplies the informers
// used for eviction, scheme resolves object GVKs, and conf lists the GVKs to
// overlay and the TTL. GVK strings are formatted as "<group>/<version>/<Kind>"
// and are resolved against scheme.
func New(inner client.Client, informers InformerSource, scheme *runtime.Scheme, conf Config) (*CachingClient, error) {
	gvks, err := resolveGVKs(scheme, conf.GVKs)
	if err != nil {
		return nil, err
	}
	ttl := conf.TTL.Duration
	if ttl <= 0 {
		ttl = defaultTTL
	}
	return &CachingClient{
		Client:    inner,
		informers: informers,
		scheme:    scheme,
		overlay:   newOverlay(ttl),
		ttl:       ttl,
		gvks:      gvks,
	}, nil
}

// resolveGVKs maps "<group>/<version>/<Kind>" strings to GVKs via the scheme's
// known types. Mirrors the resolution logic of multicluster.InitFromConf, but
// stays local to this package.
func resolveGVKs(scheme *runtime.Scheme, gvkStrs []string) (map[schema.GroupVersionKind]bool, error) {
	byStr := make(map[string]schema.GroupVersionKind)
	for gvk := range scheme.AllKnownTypes() {
		byStr[gvk.GroupVersion().String()+"/"+gvk.Kind] = gvk
	}
	out := make(map[schema.GroupVersionKind]bool, len(gvkStrs))
	for _, s := range gvkStrs {
		gvk, ok := byStr[s]
		if !ok {
			return nil, errors.New("clientcache: no gvk registered in scheme for " + s)
		}
		out[gvk] = true
	}
	return out, nil
}

// Inner returns the wrapped client, e.g. for use with a controller Builder that
// needs the raw client rather than the caching wrapper.
func (c *CachingClient) Inner() client.Client { return c.Client }

// gvkFor resolves the GVK of obj and reports whether it is cached.
func (c *CachingClient) gvkFor(obj runtime.Object) (schema.GroupVersionKind, bool) {
	gvks, _, err := c.scheme.ObjectKinds(obj)
	if err != nil || len(gvks) != 1 {
		return schema.GroupVersionKind{}, false
	}
	gvk := gvks[0]
	return gvk, c.gvks[gvk]
}

// itemGVKForList resolves the singular item GVK for a list object and reports
// whether that item GVK is cached. The list GVK's Kind ends with "List".
func (c *CachingClient) itemGVKForList(list client.ObjectList) (schema.GroupVersionKind, bool) {
	gvks, _, err := c.scheme.ObjectKinds(list)
	if err != nil || len(gvks) != 1 {
		return schema.GroupVersionKind{}, false
	}
	gvk := gvks[0]
	if kind, ok := trimListSuffix(gvk.Kind); ok {
		gvk.Kind = kind
	}
	return gvk, c.gvks[gvk]
}

// trimListSuffix strips a trailing "List" from a Kind, reporting whether it did.
func trimListSuffix(kind string) (string, bool) {
	const suffix = "List"
	if len(kind) > len(suffix) && kind[len(kind)-len(suffix):] == suffix {
		return kind[:len(kind)-len(suffix)], true
	}
	return kind, false
}

// Create delegates to the inner client and, on success for a cached GVK, adds
// the object to the overlay.
func (c *CachingClient) Create(ctx context.Context, obj client.Object, opts ...client.CreateOption) error {
	if err := c.Client.Create(ctx, obj, opts...); err != nil {
		return err
	}
	if gvk, cached := c.gvkFor(obj); cached {
		c.overlay.upsert(gvk, obj)
	}
	return nil
}

// Update delegates to the inner client and, on success for a cached GVK,
// refreshes the overlay entry.
func (c *CachingClient) Update(ctx context.Context, obj client.Object, opts ...client.UpdateOption) error {
	if err := c.Client.Update(ctx, obj, opts...); err != nil {
		return err
	}
	if gvk, cached := c.gvkFor(obj); cached {
		c.overlay.upsert(gvk, obj)
	}
	return nil
}

// Patch delegates to the inner client and, on success for a cached GVK,
// refreshes the overlay entry with the patched object.
func (c *CachingClient) Patch(ctx context.Context, obj client.Object, patch client.Patch, opts ...client.PatchOption) error {
	if err := c.Client.Patch(ctx, obj, patch, opts...); err != nil {
		return err
	}
	if gvk, cached := c.gvkFor(obj); cached {
		c.overlay.upsert(gvk, obj)
	}
	return nil
}

// Delete delegates to the inner client and, on success for a cached GVK, stores
// a tombstone in the overlay.
func (c *CachingClient) Delete(ctx context.Context, obj client.Object, opts ...client.DeleteOption) error {
	if err := c.Client.Delete(ctx, obj, opts...); err != nil {
		return err
	}
	if gvk, cached := c.gvkFor(obj); cached {
		c.overlay.remove(gvk, obj)
	}
	return nil
}

// Get delegates to the inner client, then applies the overlay: a tombstone
// yields NotFound; a live overlay entry overrides the inner result; and an
// overlay entry can satisfy a Get that the inner client reports as NotFound.
func (c *CachingClient) Get(ctx context.Context, key client.ObjectKey, obj client.Object, opts ...client.GetOption) error {
	gvk, cached := c.gvkFor(obj)
	if !cached {
		return c.Client.Get(ctx, key, obj, opts...)
	}
	err := c.Client.Get(ctx, key, obj, opts...)
	if err != nil && !apierrors.IsNotFound(err) {
		return err
	}
	e, ok := c.overlay.get(gvk, objectKey{namespace: key.Namespace, name: key.Name})
	if !ok {
		// No overlay entry: return the inner result (value or NotFound) as-is.
		return err
	}
	if e.deleted {
		return apierrors.NewNotFound(schema.GroupResource{Group: gvk.Group, Resource: gvk.Kind}, key.Name)
	}
	// Live overlay entry: copy it into obj, overriding the inner result.
	if cpErr := c.scheme.Convert(e.obj, obj, nil); cpErr != nil {
		return cpErr
	}
	return nil
}

// List delegates to the inner client, then merges the overlay into the result.
func (c *CachingClient) List(ctx context.Context, list client.ObjectList, opts ...client.ListOption) error {
	itemGVK, cached := c.itemGVKForList(list)
	if !cached {
		return c.Client.List(ctx, list, opts...)
	}
	if err := c.Client.List(ctx, list, opts...); err != nil {
		return err
	}
	items, err := meta.ExtractList(list)
	if err != nil {
		return err
	}
	lo := &client.ListOptions{}
	lo.ApplyOptions(opts)
	merged := c.overlay.overlayList(itemGVK, items, lo)
	return meta.SetList(list, merged)
}

// IndexField delegates to the inner client (if it is a FieldIndexer) and also
// registers the IndexerFunc with the overlay so overlay entries can be matched
// against MatchingFields.
func (c *CachingClient) IndexField(ctx context.Context, obj client.Object, field string, extractValue client.IndexerFunc) error {
	if indexer, ok := c.Client.(client.FieldIndexer); ok {
		if err := indexer.IndexField(ctx, obj, field, extractValue); err != nil {
			return err
		}
	}
	if gvk, cached := c.gvkFor(obj); cached {
		c.overlay.registerIndex(gvk, field, extractValue)
	}
	return nil
}

// Status returns a status writer that mirrors status Update/Patch writes for
// cached GVKs into the overlay.
func (c *CachingClient) Status() client.StatusWriter {
	return &statusWriter{c: c, inner: c.Client.Status()}
}

// statusWriter wraps the inner status writer and reflects status writes into
// the overlay for cached GVKs.
type statusWriter struct {
	c     *CachingClient
	inner client.StatusWriter
}

func (s *statusWriter) Create(ctx context.Context, obj, subResource client.Object, opts ...client.SubResourceCreateOption) error {
	return s.inner.Create(ctx, obj, subResource, opts...)
}

func (s *statusWriter) Update(ctx context.Context, obj client.Object, opts ...client.SubResourceUpdateOption) error {
	if err := s.inner.Update(ctx, obj, opts...); err != nil {
		return err
	}
	if gvk, cached := s.c.gvkFor(obj); cached {
		s.c.overlay.upsert(gvk, obj)
	}
	return nil
}

func (s *statusWriter) Patch(ctx context.Context, obj client.Object, patch client.Patch, opts ...client.SubResourcePatchOption) error {
	if err := s.inner.Patch(ctx, obj, patch, opts...); err != nil {
		return err
	}
	if gvk, cached := s.c.gvkFor(obj); cached {
		s.c.overlay.upsert(gvk, obj)
	}
	return nil
}

func (s *statusWriter) Apply(ctx context.Context, obj runtime.ApplyConfiguration, opts ...client.SubResourceApplyOption) error {
	return s.inner.Apply(ctx, obj, opts...)
}
