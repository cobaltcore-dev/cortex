// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package clientcache

import (
	"context"
	"errors"
	"strconv"
	"sync"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/fields"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// entry is a single overlaid object with the bookkeeping needed for eviction.
type entry struct {
	obj             client.Object
	uid             types.UID
	resourceVersion string
	deleted         bool // tombstone: object was deleted through the caching client
	expiresAt       time.Time
}

// objectKey identifies an object within a GVK by namespace and name.
type objectKey struct {
	namespace string
	name      string
}

func keyForObject(obj client.Object) objectKey {
	return objectKey{namespace: obj.GetNamespace(), name: obj.GetName()}
}

// lockKey identifies the object whose write path a keyedMutex serializes.
type lockKey struct {
	gvk schema.GroupVersionKind
	key objectKey
}

// refMutex is the actual per-object lock plus a reference count of how many
// callers currently hold or are waiting for it. The count lets keyedMutex know
// when the entry is unused so it can be deleted (see keyedMutex.lock).
type refMutex struct {
	mu  sync.Mutex
	ref int
}

// keyedMutex hands out one mutex per key, serializing operations that share a
// key while letting distinct keys proceed concurrently.
//
// A sync.Map (or a map we only ever insert into) would be simpler, but it would leak:
// this cache wraps a single client for the whole lifetime of the controller-manager process,
// and a controller reconciles a continuous stream of (often short-lived) objects.
// A map would therefore accumulate one mutex per distinct object ever written and
// grow without bound. To avoid that we reference count each entry and delete it
// once the last holder unlocks, so the map only ever holds locks for objects
// with writes currently in flight.
//
// The two mutexes have distinct, non-overlapping roles:
//   - k.mu guards the locks map itself. It is only ever held for the tiny
//     bookkeeping critical sections below (map lookup/insert/delete and the
//     ref counter), never across the caller's I/O.
//   - rm.mu is the real per-object lock, held by the caller across the inner
//     client call and the overlay mutation.
//
// Because k.mu is never held while rm.mu is locked (we release k.mu before
// taking rm.mu), the two can never deadlock against each other.
type keyedMutex struct {
	mu    sync.Mutex
	locks map[lockKey]*refMutex
}

func newKeyedMutex() *keyedMutex {
	return &keyedMutex{locks: make(map[lockKey]*refMutex)}
}

// lock acquires the per-key mutex and returns a function that releases it.
func (k *keyedMutex) lock(lk lockKey) func() {
	// Look up (or create) the entry for this key and register our interest by
	// bumping ref, all under k.mu so the map stays consistent. We increment ref
	// here, before taking rm.mu, so that a concurrent unlock cannot see ref==0
	// and delete the entry out from under us while we are blocked waiting on it.
	k.mu.Lock()
	rm := k.locks[lk]
	if rm == nil {
		rm = &refMutex{}
		k.locks[lk] = rm
	}
	rm.ref++
	k.mu.Unlock()

	// Take the real per-object lock outside k.mu; this is where a second caller
	// for the same key blocks (and where we may block across the caller's I/O).
	rm.mu.Lock()
	return func() {
		rm.mu.Unlock()
		// Drop our reference and, if we were the last holder, remove the entry
		// so the map does not grow unbounded.
		k.mu.Lock()
		rm.ref--
		if rm.ref == 0 {
			delete(k.locks, lk)
		}
		k.mu.Unlock()
	}
}

// resourceVersionAtLeast reports whether observed >= cached, treating
// ResourceVersions as opaque monotonically increasing integers (as the
// kubernetes apiserver guarantees per resource). Unparsable or empty values
// are treated conservatively: an empty cached RV means "evict on any sighting".
func resourceVersionAtLeast(observed, cached string) bool {
	if cached == "" {
		return true
	}
	if observed == "" {
		return false
	}
	oi, oerr := strconv.ParseUint(observed, 10, 64)
	ci, cerr := strconv.ParseUint(cached, 10, 64)
	if oerr != nil || cerr != nil {
		// Fall back to string comparison if not integers.
		return observed >= cached
	}
	return oi >= ci
}

// defaultTTL is used when Config.TTL is zero.
const defaultTTL = 2 * time.Minute

// CachingClient wraps an inner client.Client with a transparent in-process
// overlay. Writes populate the overlay; reads merge the overlay with the inner
// (informer-backed) result; entries are evicted once the real object appears in
// an informer (see runnable.go) or after TTL expiry.
//
// It embeds client.Client so all methods not overridden below are delegated to
// the inner client unchanged.
//
// The local overlay maps each cached GVK to a set of entries keyed by
// namespace/name. An entry is either live (a pending write not yet visible in
// the informer) or a tombstone (a Delete that has not yet propagated). Reads
// merge the inner result with these entries: tombstones suppress objects;
// live entries override or supplement the inner result.
type CachingClient struct {
	client.Client // inner client, used for delegation

	inner  Client
	scheme *runtime.Scheme
	ttl    time.Duration
	gvks   map[schema.GroupVersionKind]bool

	mu       sync.RWMutex
	byGVK    map[schema.GroupVersionKind]map[objectKey]*entry
	indexers map[schema.GroupVersionKind]map[string]client.IndexerFunc

	// writeLocks serializes writes to the same object so the inner call and the
	// overlay mutation are atomic per object, keeping the overlay from falling
	// behind the apiserver under concurrent writes.
	writeLocks *keyedMutex
}

// New builds a CachingClient wrapping inner. informers supplies the informers
// used for eviction, scheme resolves object GVKs, and conf lists the GVKs to
// overlay and the TTL. GVK strings are formatted as "<group>/<version>/<Kind>"
// and are resolved against scheme.
func New(inner Client, scheme *runtime.Scheme, conf Config) (*CachingClient, error) {
	gvks, err := resolveGVKs(scheme, conf.GVKs)
	if err != nil {
		return nil, err
	}
	ttl := conf.TTL.Duration
	if ttl <= 0 {
		ttl = defaultTTL
	}
	return &CachingClient{
		Client:     inner,
		inner:      inner,
		scheme:     scheme,
		ttl:        ttl,
		gvks:       gvks,
		byGVK:      make(map[schema.GroupVersionKind]map[objectKey]*entry),
		indexers:   make(map[schema.GroupVersionKind]map[string]client.IndexerFunc),
		writeLocks: newKeyedMutex(),
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

// upsert stores a live (non-tombstone) entry for the object.
func (c *CachingClient) upsert(gvk schema.GroupVersionKind, obj client.Object) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.ensureGVK(gvk)
	c.byGVK[gvk][keyForObject(obj)] = &entry{
		obj:             obj.DeepCopyObject().(client.Object),
		uid:             obj.GetUID(),
		resourceVersion: obj.GetResourceVersion(),
		deleted:         false,
		expiresAt:       time.Now().Add(c.ttl),
	}
}

// tombstone marks the object as deleted in the overlay so it is filtered out
// of reads until the deletion is observed in an informer.
func (c *CachingClient) tombstone(gvk schema.GroupVersionKind, obj client.Object) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.ensureGVK(gvk)
	c.byGVK[gvk][keyForObject(obj)] = &entry{
		obj:             obj.DeepCopyObject().(client.Object),
		uid:             obj.GetUID(),
		resourceVersion: obj.GetResourceVersion(),
		deleted:         true,
		expiresAt:       time.Now().Add(c.ttl),
	}
}

// evictIfSeen removes the overlay entry for obj if the informer-observed object
// matches by UID and its ResourceVersion is at least as new as the cached one.
func (c *CachingClient) evictIfSeen(gvk schema.GroupVersionKind, obj client.Object) {
	c.mu.Lock()
	defer c.mu.Unlock()
	entries, ok := c.byGVK[gvk]
	if !ok {
		return
	}
	key := keyForObject(obj)
	e, ok := entries[key]
	if !ok {
		return
	}
	// Only evict when the informer sees the same object generation (by UID) at
	// a ResourceVersion >= the one we cached. Otherwise the informer might be
	// showing an older revision than our pending write.
	if e.uid != "" && obj.GetUID() != "" && e.uid != obj.GetUID() {
		return
	}
	if !resourceVersionAtLeast(obj.GetResourceVersion(), e.resourceVersion) {
		return
	}
	delete(entries, key)
}

// getEntry returns the overlay entry for the key, if present.
func (c *CachingClient) getEntry(gvk schema.GroupVersionKind, key objectKey) (*entry, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	entries, ok := c.byGVK[gvk]
	if !ok {
		return nil, false
	}
	e, ok := entries[key]
	return e, ok
}

// cleanupExpired removes entries whose TTL has passed.
func (c *CachingClient) cleanupExpired(now time.Time) {
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, entries := range c.byGVK {
		for key, e := range entries {
			if now.After(e.expiresAt) {
				delete(entries, key)
			}
		}
	}
}

// registerIndex captures an IndexerFunc for a field so overlay entries can be
// matched against MatchingFields queries.
func (c *CachingClient) registerIndex(gvk schema.GroupVersionKind, field string, fn client.IndexerFunc) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.indexers[gvk] == nil {
		c.indexers[gvk] = make(map[string]client.IndexerFunc)
	}
	c.indexers[gvk][field] = fn
}

// ensureGVK initialises the per-GVK entry map if absent. Callers must hold the write lock.
func (c *CachingClient) ensureGVK(gvk schema.GroupVersionKind) {
	if c.byGVK[gvk] == nil {
		c.byGVK[gvk] = make(map[objectKey]*entry)
	}
}

// overlayList merges the overlay entries for the GVK into the informer result,
// deduplicating by objectKey (overlay wins), dropping tombstones, and filtering
// overlay-only entries against the list options' label and field selectors.
func (c *CachingClient) overlayList(gvk schema.GroupVersionKind, existing []runtime.Object, lo *client.ListOptions) []runtime.Object {
	c.mu.RLock()
	defer c.mu.RUnlock()
	entries := c.byGVK[gvk]
	if len(entries) == 0 {
		return existing
	}

	result := make([]runtime.Object, 0, len(existing)+len(entries))
	// Track which overlay keys are handled so overlay-only entries can be added.
	handled := make(map[objectKey]bool, len(entries))

	for _, item := range existing {
		obj, ok := item.(client.Object)
		if !ok {
			result = append(result, item)
			continue
		}
		key := keyForObject(obj)
		e, present := entries[key]
		if !present {
			result = append(result, item)
			continue
		}
		handled[key] = true
		// Overlay wins over the informer result for the same key.
		if e.deleted {
			// Tombstone: drop the object entirely.
			continue
		}
		// The inner result matched the query against the informer's (old) field
		// values. Re-check the overlay version: if a write changed a queried
		// field, the overlay object no longer belongs in this result set.
		if !c.matchesLocked(gvk, e.obj, lo) {
			continue
		}
		result = append(result, e.obj.DeepCopyObject())
	}

	// Add overlay-only entries (not present in the informer result) that match
	// the list options.
	for key, e := range entries {
		if handled[key] {
			continue
		}
		if e.deleted {
			continue
		}
		if !c.matchesLocked(gvk, e.obj, lo) {
			continue
		}
		result = append(result, e.obj.DeepCopyObject())
	}
	return result
}

// matchesLocked reports whether obj satisfies the list options' namespace,
// label and field selectors. Callers must hold at least the read lock.
func (c *CachingClient) matchesLocked(gvk schema.GroupVersionKind, obj client.Object, lo *client.ListOptions) bool {
	if lo == nil {
		return true
	}
	if lo.Namespace != "" && obj.GetNamespace() != lo.Namespace {
		return false
	}
	if lo.LabelSelector != nil && !lo.LabelSelector.Matches(labels.Set(obj.GetLabels())) {
		return false
	}
	if lo.FieldSelector != nil && !lo.FieldSelector.Empty() {
		set := c.fieldSetLocked(gvk, obj)
		if !lo.FieldSelector.Matches(set) {
			return false
		}
	}
	return true
}

// fieldSetLocked builds a fields.Set for obj using the registered IndexerFuncs
// for the GVK. Callers must hold at least the read lock.
func (c *CachingClient) fieldSetLocked(gvk schema.GroupVersionKind, obj client.Object) fields.Set {
	set := fields.Set{}
	for field, fn := range c.indexers[gvk] {
		for _, v := range fn(obj) {
			// A field selector matches a single value; take the first indexed
			// value for the field (mirrors controller-runtime cache behaviour).
			set[field] = v
			break
		}
	}
	return set
}

// Create delegates to the inner client and, on success for a cached GVK, adds
// the object to the overlay.
func (c *CachingClient) Create(ctx context.Context, obj client.Object, opts ...client.CreateOption) error {
	gvk, cached := c.gvkFor(obj)
	if !cached {
		return c.Client.Create(ctx, obj, opts...)
	}
	unlock := c.writeLocks.lock(lockKey{gvk: gvk, key: keyForObject(obj)})
	defer unlock()
	if err := c.Client.Create(ctx, obj, opts...); err != nil {
		return err
	}
	c.upsert(gvk, obj)
	return nil
}

// Update delegates to the inner client and, on success for a cached GVK,
// refreshes the overlay entry.
func (c *CachingClient) Update(ctx context.Context, obj client.Object, opts ...client.UpdateOption) error {
	gvk, cached := c.gvkFor(obj)
	if !cached {
		return c.Client.Update(ctx, obj, opts...)
	}
	unlock := c.writeLocks.lock(lockKey{gvk: gvk, key: keyForObject(obj)})
	defer unlock()
	if err := c.Client.Update(ctx, obj, opts...); err != nil {
		return err
	}
	c.upsert(gvk, obj)
	return nil
}

// Patch delegates to the inner client and, on success for a cached GVK,
// refreshes the overlay entry with the patched object.
func (c *CachingClient) Patch(ctx context.Context, obj client.Object, patch client.Patch, opts ...client.PatchOption) error {
	gvk, cached := c.gvkFor(obj)
	if !cached {
		return c.Client.Patch(ctx, obj, patch, opts...)
	}
	unlock := c.writeLocks.lock(lockKey{gvk: gvk, key: keyForObject(obj)})
	defer unlock()
	if err := c.Client.Patch(ctx, obj, patch, opts...); err != nil {
		return err
	}
	c.upsert(gvk, obj)
	return nil
}

// Delete delegates to the inner client and, on success for a cached GVK, stores
// a tombstone in the overlay. The object is NOT immediately removed from the
// local map; it stays as a deleted=true entry until the deletion propagates
// through the informer (which triggers eviction) or the TTL expires. This
// ensures that reads between the Delete call and the informer event correctly
// return NotFound rather than serving a stale object from the informer cache.
func (c *CachingClient) Delete(ctx context.Context, obj client.Object, opts ...client.DeleteOption) error {
	gvk, cached := c.gvkFor(obj)
	if !cached {
		return c.Client.Delete(ctx, obj, opts...)
	}
	unlock := c.writeLocks.lock(lockKey{gvk: gvk, key: keyForObject(obj)})
	defer unlock()
	if err := c.Client.Delete(ctx, obj, opts...); err != nil {
		return err
	}
	c.tombstone(gvk, obj)
	return nil
}

// DeleteAllOf delegates to the inner client and, on success for a cached GVK,
// tombstones all overlay entries that match the delete options. Objects that
// live only in the informer cache (not in the overlay) will be evicted
// naturally once the deletion propagates through the informer.
func (c *CachingClient) DeleteAllOf(ctx context.Context, obj client.Object, opts ...client.DeleteAllOfOption) error {
	gvk, cached := c.gvkFor(obj)
	if !cached {
		return c.Client.DeleteAllOf(ctx, obj, opts...)
	}
	if err := c.Client.DeleteAllOf(ctx, obj, opts...); err != nil {
		return err
	}
	dao := &client.DeleteAllOfOptions{}
	dao.ApplyOptions(opts)
	c.mu.Lock()
	defer c.mu.Unlock()
	for key, e := range c.byGVK[gvk] {
		if !c.matchesLocked(gvk, e.obj, &dao.ListOptions) {
			continue
		}
		c.byGVK[gvk][key] = &entry{
			obj:             e.obj,
			uid:             e.uid,
			resourceVersion: e.resourceVersion,
			deleted:         true,
			expiresAt:       time.Now().Add(c.ttl),
		}
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
	e, ok := c.getEntry(gvk, objectKey{namespace: key.Namespace, name: key.Name})
	if !ok {
		// No overlay entry: return the inner result (value or NotFound) as-is.
		return err
	}
	if e.deleted {
		return apierrors.NewNotFound(schema.GroupResource{Group: gvk.Group, Resource: gvk.Kind}, key.Name)
	}
	// Live overlay entry: copy it into obj, overriding the inner result.
	// Deep-copy the cached object first so scheme.Convert cannot alias the
	// overlay entry's maps, slices, or metadata into the caller's obj (which
	// would let callers mutate the shared cache entry).
	if cpErr := c.scheme.Convert(e.obj.DeepCopyObject(), obj, nil); cpErr != nil {
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
	merged := c.overlayList(itemGVK, items, lo)
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
		c.registerIndex(gvk, field, extractValue)
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
	gvk, cached := s.c.gvkFor(obj)
	if !cached {
		return s.inner.Update(ctx, obj, opts...)
	}
	unlock := s.c.writeLocks.lock(lockKey{gvk: gvk, key: keyForObject(obj)})
	defer unlock()
	if err := s.inner.Update(ctx, obj, opts...); err != nil {
		return err
	}
	s.c.upsert(gvk, obj)
	return nil
}

func (s *statusWriter) Patch(ctx context.Context, obj client.Object, patch client.Patch, opts ...client.SubResourcePatchOption) error {
	gvk, cached := s.c.gvkFor(obj)
	if !cached {
		return s.inner.Patch(ctx, obj, patch, opts...)
	}
	unlock := s.c.writeLocks.lock(lockKey{gvk: gvk, key: keyForObject(obj)})
	defer unlock()
	if err := s.inner.Patch(ctx, obj, patch, opts...); err != nil {
		return err
	}
	s.c.upsert(gvk, obj)
	return nil
}

func (s *statusWriter) Apply(ctx context.Context, obj runtime.ApplyConfiguration, opts ...client.SubResourceApplyOption) error {
	return s.inner.Apply(ctx, obj, opts...)
}
