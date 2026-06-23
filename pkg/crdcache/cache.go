// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package crdcache

import (
	"sync"
	"time"

	"sigs.k8s.io/controller-runtime/pkg/client"
)

type entry struct {
	obj   client.Object
	timer *time.Timer
}

// CRDCache is a thread-safe in-process map of assumed objects keyed by "<namespace>/<name>".
// It is not a client itself; use CachingClient for the wrapped client.Client interface.
type CRDCache struct {
	mu      sync.RWMutex
	entries map[string]*entry
	opts    Options
}

// New creates a CRDCache with the given options.
func New(opts Options) *CRDCache {
	return &CRDCache{
		entries: make(map[string]*entry),
		opts:    opts,
	}
}

// Assume stores a deep copy of obj in the cache and starts (or resets) the TTL timer.
func (c *CRDCache) Assume(obj client.Object) {
	key := ObjectKey(obj)
	copied := obj.DeepCopyObject().(client.Object)
	ttl := c.opts.ttl()

	c.mu.Lock()
	defer c.mu.Unlock()

	if existing, ok := c.entries[key]; ok {
		existing.timer.Stop()
	}
	timer := time.AfterFunc(ttl, func() { c.Forget(key) })
	c.entries[key] = &entry{obj: copied, timer: timer}
}

// Forget removes the entry for key from the cache and stops its TTL timer.
// It is a no-op if the key is not present.
func (c *CRDCache) Forget(key string) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if e, ok := c.entries[key]; ok {
		e.timer.Stop()
		delete(c.entries, key)
	}
}

// Get returns the cached object for the given key, or (nil, false) if absent.
func (c *CRDCache) Get(key string) (client.Object, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()

	e, ok := c.entries[key]
	if !ok {
		return nil, false
	}
	return e.obj.DeepCopyObject().(client.Object), true
}

// List returns a snapshot of all currently-cached objects.
func (c *CRDCache) List() []client.Object {
	c.mu.RLock()
	defer c.mu.RUnlock()

	out := make([]client.Object, 0, len(c.entries))
	for _, e := range c.entries {
		out = append(out, e.obj.DeepCopyObject().(client.Object))
	}
	return out
}

// ObjectKey returns the cache key for an object: "<namespace>/<name>".
func ObjectKey(obj client.Object) string {
	return obj.GetNamespace() + "/" + obj.GetName()
}
