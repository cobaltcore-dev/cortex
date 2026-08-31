// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package cache

import (
	"sync"

	"k8s.io/apimachinery/pkg/runtime/schema"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// lockKey identifies the object whose write path a keyedMutex serializes.
type lockKey struct {
	gvk schema.GroupVersionKind
	key client.ObjectKey
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
