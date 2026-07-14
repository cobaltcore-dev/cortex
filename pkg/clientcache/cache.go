// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package clientcache

import (
	"strconv"
	"sync"
	"time"

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

// overlay is the generic, client-independent core of the cache. It stores
// pending writes keyed by GVK and objectKey and merges them into informer
// read results until the real object is observed in an informer (eviction).
type overlay struct {
	mu    sync.RWMutex
	byGVK map[schema.GroupVersionKind]map[objectKey]*entry
	ttl   time.Duration
	// indexers holds the IndexerFunc per field per GVK, captured from
	// IndexField calls, so overlay entries can be matched against FieldSelectors.
	indexers map[schema.GroupVersionKind]map[string]client.IndexerFunc
}

func newOverlay(ttl time.Duration) *overlay {
	return &overlay{
		byGVK:    make(map[schema.GroupVersionKind]map[objectKey]*entry),
		ttl:      ttl,
		indexers: make(map[schema.GroupVersionKind]map[string]client.IndexerFunc),
	}
}

func keyForObject(obj client.Object) objectKey {
	return objectKey{namespace: obj.GetNamespace(), name: obj.GetName()}
}

// upsert stores a live (non-tombstone) entry for the object.
func (o *overlay) upsert(gvk schema.GroupVersionKind, obj client.Object) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.ensureGVK(gvk)
	o.byGVK[gvk][keyForObject(obj)] = &entry{
		obj:             obj.DeepCopyObject().(client.Object),
		uid:             obj.GetUID(),
		resourceVersion: obj.GetResourceVersion(),
		deleted:         false,
		expiresAt:       time.Now().Add(o.ttl),
	}
}

// remove stores a tombstone so the object is filtered out of reads until the
// deletion is observed in an informer.
func (o *overlay) remove(gvk schema.GroupVersionKind, obj client.Object) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.ensureGVK(gvk)
	o.byGVK[gvk][keyForObject(obj)] = &entry{
		obj:             obj.DeepCopyObject().(client.Object),
		uid:             obj.GetUID(),
		resourceVersion: obj.GetResourceVersion(),
		deleted:         true,
		expiresAt:       time.Now().Add(o.ttl),
	}
}

// evictIfSeen removes the overlay entry for obj if the informer-observed object
// matches by UID and its ResourceVersion is at least as new as the cached one.
func (o *overlay) evictIfSeen(gvk schema.GroupVersionKind, obj client.Object) {
	o.mu.Lock()
	defer o.mu.Unlock()
	entries, ok := o.byGVK[gvk]
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

// get returns the overlay entry for the key, if present.
func (o *overlay) get(gvk schema.GroupVersionKind, key objectKey) (*entry, bool) {
	o.mu.RLock()
	defer o.mu.RUnlock()
	entries, ok := o.byGVK[gvk]
	if !ok {
		return nil, false
	}
	e, ok := entries[key]
	return e, ok
}

// overlayList merges the overlay entries for the GVK into the informer result,
// deduplicating by objectKey (overlay wins), dropping tombstones, and filtering
// overlay-only entries against the list options' label and field selectors.
func (o *overlay) overlayList(gvk schema.GroupVersionKind, existing []runtime.Object, lo *client.ListOptions) []runtime.Object {
	o.mu.RLock()
	defer o.mu.RUnlock()
	entries := o.byGVK[gvk]
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
		if !o.matchesLocked(gvk, e.obj, lo) {
			continue
		}
		result = append(result, e.obj.DeepCopyObject())
	}
	return result
}

// matchesLocked reports whether obj satisfies the list options' namespace,
// label and field selectors. Callers must hold at least the read lock.
func (o *overlay) matchesLocked(gvk schema.GroupVersionKind, obj client.Object, lo *client.ListOptions) bool {
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
		set := o.fieldSetLocked(gvk, obj)
		if !lo.FieldSelector.Matches(set) {
			return false
		}
	}
	return true
}

// fieldSetLocked builds a fields.Set for obj using the registered IndexerFuncs
// for the GVK. Callers must hold at least the read lock.
func (o *overlay) fieldSetLocked(gvk schema.GroupVersionKind, obj client.Object) fields.Set {
	set := fields.Set{}
	for field, fn := range o.indexers[gvk] {
		for _, v := range fn(obj) {
			// A field selector matches a single value; take the first indexed
			// value for the field (mirrors controller-runtime cache behaviour).
			set[field] = v
			break
		}
	}
	return set
}

// registerIndex captures an IndexerFunc for a field so overlay entries can be
// matched against MatchingFields queries.
func (o *overlay) registerIndex(gvk schema.GroupVersionKind, field string, fn client.IndexerFunc) {
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.indexers[gvk] == nil {
		o.indexers[gvk] = make(map[string]client.IndexerFunc)
	}
	o.indexers[gvk][field] = fn
}

// cleanupExpired removes entries whose TTL has passed.
func (o *overlay) cleanupExpired(now time.Time) {
	o.mu.Lock()
	defer o.mu.Unlock()
	for _, entries := range o.byGVK {
		for key, e := range entries {
			if now.After(e.expiresAt) {
				delete(entries, key)
			}
		}
	}
}

func (o *overlay) ensureGVK(gvk schema.GroupVersionKind) {
	if o.byGVK[gvk] == nil {
		o.byGVK[gvk] = make(map[objectKey]*entry)
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
