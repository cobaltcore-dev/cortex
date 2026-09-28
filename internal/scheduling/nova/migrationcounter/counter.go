// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

// Package migrationcounter provides a lightweight, in-memory counter for
// repeated migration scheduling requests (currently evacuations).
//
// It counts how often the same migration request is seen (keyed by VM UUID,
// optionally combined with the source host). It is a pure counter: the policy
// decision of "how many repeats qualify a VM" lives with the caller (the
// pipeline controller), which compares the count against a configured
// threshold.
//
// State is kept entirely in memory and intentionally does not survive a pod
// restart. Memory is bounded via a soft entry cap and a throttled lazy sweep of
// stale entries.
package migrationcounter

import (
	"sort"
	"sync"
	"time"
)

// Config configures a RepeatedMigrationCounter.
type Config struct {
	// Window is the relevance window for counting. If an entry has not been
	// observed for longer than Window, its count restarts on the next Observe
	// and it is treated as absent by Count. Entries older than Window are also
	// evicted during a sweep.
	Window time.Duration
	// CleanupInterval is the minimum time between throttled lazy sweeps of
	// stale entries. Sweeps are triggered from Observe, never more often than
	// this.
	CleanupInterval time.Duration
	// MaxEntries is the soft cap on the number of tracked entries. When
	// exceeded on insert (and the sweep did not bring the map back under the
	// cap), the oldest third of entries (by last-seen time) are evicted.
	MaxEntries int
}

type entry struct {
	count    int
	lastSeen time.Time
}

// RepeatedMigrationCounter counts repeated migration requests in memory.
type RepeatedMigrationCounter struct {
	config Config
	// now allows overriding time in tests. Defaults to time.Now.
	now func() time.Time

	mu          sync.Mutex
	entries     map[string]*entry
	lastCleanup time.Time
}

// New creates a new RepeatedMigrationCounter with the given config.
func New(config Config) *RepeatedMigrationCounter {
	return &RepeatedMigrationCounter{
		config:  config,
		now:     time.Now,
		entries: make(map[string]*entry),
	}
}

// Len returns the current number of tracked entries. Safe for concurrent use;
// used to expose a gauge without the tracker depending on Prometheus.
func (t *RepeatedMigrationCounter) Len() int {
	t.mu.Lock()
	defer t.mu.Unlock()
	return len(t.entries)
}

// key builds the tracking key for a migration request. The VM UUID is the
// reliable primary key; the source host, when cleanly derivable from the Nova
// request's ignore_hosts list, is folded in to distinguish repeated migrations
// of the same VM from different hosts. During an evacuate, Nova adds the source
// host to ignore_hosts; we only trust it as the source when it is the single
// entry, to avoid mis-keying when the list contains additional hosts.
func key(vmUUID string, ignoreHosts *[]string) string {
	if ignoreHosts != nil && len(*ignoreHosts) == 1 {
		return (*ignoreHosts)[0] + "|" + vmUUID
	}
	return vmUUID
}

// Observe records a single migration request for the given VM and returns the
// current count within the window.
func (t *RepeatedMigrationCounter) Observe(vmUUID string, ignoreHosts *[]string) (count int) {
	k := key(vmUUID, ignoreHosts)
	if k == "" {
		return 0
	}
	now := t.now()

	t.mu.Lock()
	defer t.mu.Unlock()

	t.maybeSweepLocked(now)

	e, ok := t.entries[k]
	switch {
	case !ok:
		e = &entry{}
		t.entries[k] = e
	case now.Sub(e.lastSeen) > t.config.Window:
		// Stale entry: restart the count for a fresh incident window.
		e.count = 0
	}
	e.count++
	e.lastSeen = now

	t.enforceMaxEntriesLocked()

	return e.count
}

// Count returns the current count for the given VM within the relevance window,
// or 0 if the key is absent or stale.
func (t *RepeatedMigrationCounter) Count(vmUUID string, ignoreHosts *[]string) int {
	k := key(vmUUID, ignoreHosts)
	if k == "" {
		return 0
	}
	now := t.now()

	t.mu.Lock()
	defer t.mu.Unlock()

	e, ok := t.entries[k]
	if !ok {
		return 0
	}
	if now.Sub(e.lastSeen) > t.config.Window {
		return 0
	}
	return e.count
}

// maybeSweepLocked deletes stale entries (older than Window), but at most once
// per CleanupInterval. Callers must hold t.mu.
func (t *RepeatedMigrationCounter) maybeSweepLocked(now time.Time) {
	if t.config.CleanupInterval > 0 && now.Sub(t.lastCleanup) < t.config.CleanupInterval {
		return
	}
	t.lastCleanup = now
	if t.config.Window <= 0 {
		return
	}
	for key, e := range t.entries {
		if now.Sub(e.lastSeen) > t.config.Window {
			delete(t.entries, key)
		}
	}
}

// enforceMaxEntriesLocked evicts the oldest third of entries (by last-seen
// time) when the map exceeds MaxEntries. This only fires when the throttled
// sweep did not already bring the map back under the cap. Bulk-evicting a third
// amortizes the sort cost and leaves headroom so the cap is not hit on every
// insert. Callers must hold t.mu.
func (t *RepeatedMigrationCounter) enforceMaxEntriesLocked() {
	if t.config.MaxEntries <= 0 || len(t.entries) <= t.config.MaxEntries {
		return
	}
	type keyed struct {
		key      string
		lastSeen time.Time
	}
	all := make([]keyed, 0, len(t.entries))
	for key, e := range t.entries {
		all = append(all, keyed{key: key, lastSeen: e.lastSeen})
	}
	sort.Slice(all, func(i, j int) bool { return all[i].lastSeen.Before(all[j].lastSeen) })
	toEvict := (len(all) + 2) / 3 // ceil(len/3)
	for i := range toEvict {
		delete(t.entries, all[i].key)
	}
}
