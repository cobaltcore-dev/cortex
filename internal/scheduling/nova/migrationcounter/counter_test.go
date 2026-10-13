// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package migrationcounter

import (
	"fmt"
	"sync"
	"testing"
	"time"
)

func testConfig() Config {
	return Config{
		Window:          10 * time.Minute,
		CleanupInterval: time.Hour,
		MaxEntries:      100,
	}
}

// hosts returns a *[]string for use as the ignoreHosts argument.
func hosts(names ...string) *[]string { return &names }

func TestObserveCounts(t *testing.T) {
	c := New(testConfig())
	for i := 1; i <= 3; i++ {
		if got := c.Observe("vm-1", hosts("host-a")); got != i {
			t.Fatalf("expected count %d, got %d", i, got)
		}
	}
	if got := c.Count("vm-1", hosts("host-a")); got != 3 {
		t.Fatalf("expected Count 3, got %d", got)
	}
}

func TestLen(t *testing.T) {
	c := New(testConfig())
	if got := c.Len(); got != 0 {
		t.Fatalf("expected empty counter Len 0, got %d", got)
	}
	c.Observe("vm-1", nil)
	c.Observe("vm-1", nil) // same key, no new entry
	c.Observe("vm-2", nil)
	if got := c.Len(); got != 2 {
		t.Fatalf("expected Len 2, got %d", got)
	}
}

func TestEmptyUUIDIsNoOp(t *testing.T) {
	// The counter has no disabled mode; "off" means the counter is nil at the
	// call sites (handler/controller nil-guards). The only no-op input here is
	// an empty VM UUID with no source host.
	c := New(testConfig())
	if got := c.Observe("", nil); got != 0 {
		t.Fatalf("empty key Observe should be 0, got %d", got)
	}
	if got := c.Count("", nil); got != 0 {
		t.Fatalf("empty key Count should be 0, got %d", got)
	}
}

func TestWindowResetsStaleCount(t *testing.T) {
	c := New(testConfig())
	base := time.Unix(0, 0)
	c.now = func() time.Time { return base }
	c.Observe("vm-1", nil)
	c.Observe("vm-1", nil) // count = 2

	// Advance beyond the window: Count treats it as absent, and the next
	// Observe restarts the count.
	c.now = func() time.Time { return base.Add(11 * time.Minute) }
	if got := c.Count("vm-1", nil); got != 0 {
		t.Fatalf("stale entry should report Count 0, got %d", got)
	}
	if got := c.Observe("vm-1", nil); got != 1 {
		t.Fatalf("expected count reset to 1 after window, got %d", got)
	}
}

func TestSweepEvictsStaleOnCleanup(t *testing.T) {
	c := New(testConfig())
	base := time.Unix(0, 0)
	c.now = func() time.Time { return base }
	c.Observe("old-vm", nil)

	// Advance beyond both window and cleanup interval, then observe a different
	// key to trigger a throttled sweep.
	c.now = func() time.Time { return base.Add(2 * time.Hour) }
	c.Observe("new-vm", nil)

	c.mu.Lock()
	_, oldExists := c.entries[key("old-vm", nil)]
	c.mu.Unlock()
	if oldExists {
		t.Fatal("expected stale entry to be evicted by sweep")
	}
}

func TestSweepThrottledByCleanupInterval(t *testing.T) {
	c := New(testConfig())
	base := time.Unix(0, 0)
	c.now = func() time.Time { return base }
	c.Observe("old-vm", nil)

	// Past window but within the cleanup interval: sweep must NOT run yet.
	c.now = func() time.Time { return base.Add(30 * time.Minute) }
	c.Observe("new-vm", nil)

	c.mu.Lock()
	_, oldExists := c.entries[key("old-vm", nil)]
	c.mu.Unlock()
	if !oldExists {
		t.Fatal("sweep should be throttled within cleanup interval; stale entry must remain")
	}
}

func TestMaxEntriesEvictsOldestThird(t *testing.T) {
	cfg := testConfig()
	cfg.MaxEntries = 9
	cfg.Window = time.Hour          // keep entries in-window
	cfg.CleanupInterval = time.Hour // avoid sweeps interfering
	c := New(cfg)
	base := time.Unix(0, 0)
	// Insert 10 distinct keys at increasing timestamps. On the 10th insert the
	// map exceeds the cap (10 > 9) and the oldest third (ceil(10/3)=4) is evicted.
	for i := range 10 {
		ts := base.Add(time.Duration(i) * time.Second)
		c.now = func() time.Time { return ts }
		c.Observe(fmt.Sprintf("vm-%02d", i), nil)
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(c.entries) != 6 {
		t.Fatalf("expected 6 entries after 1/3 eviction, got %d", len(c.entries))
	}
	for i := range 4 {
		if _, ok := c.entries[key(fmt.Sprintf("vm-%02d", i), nil)]; ok {
			t.Fatalf("expected oldest entry vm-%02d to be evicted", i)
		}
	}
	if _, ok := c.entries[key("vm-04", nil)]; !ok {
		t.Fatal("expected vm-04 to remain after 1/3 eviction")
	}
}

func TestKeyComposition(t *testing.T) {
	if got := key("vm", nil); got != "vm" {
		t.Fatalf("nil ignore should yield bare uuid, got %q", got)
	}
	if got := key("vm", hosts()); got != "vm" {
		t.Fatalf("empty ignore should yield bare uuid, got %q", got)
	}
	if got := key("vm", hosts("host-a")); got != "host-a|vm" {
		t.Fatalf("single ignore host should compose, got %q", got)
	}
	// Multiple hosts are NOT trusted as the source (bare UUID keying).
	if got := key("vm", hosts("host-a", "host-b")); got != "vm" {
		t.Fatalf("multiple ignore hosts should yield bare uuid, got %q", got)
	}
}

func TestObserveDistinguishesBySourceHost(t *testing.T) {
	c := New(testConfig())
	c.Observe("vm-1", hosts("host-a"))
	c.Observe("vm-1", hosts("host-b"))
	// Different single source hosts key separately.
	if got := c.Count("vm-1", hosts("host-a")); got != 1 {
		t.Fatalf("expected count 1 for host-a, got %d", got)
	}
	if got := c.Count("vm-1", hosts("host-b")); got != 1 {
		t.Fatalf("expected count 1 for host-b, got %d", got)
	}
	// Bare UUID (nil / multi host) is a distinct key from the host-composite.
	if got := c.Count("vm-1", nil); got != 0 {
		t.Fatalf("expected count 0 for bare uuid, got %d", got)
	}
}

func TestConcurrentObserve(t *testing.T) {
	c := New(testConfig())
	var wg sync.WaitGroup
	for range 50 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for range 20 {
				c.Observe("vm-shared", nil)
				_ = c.Count("vm-shared", nil)
			}
		}()
	}
	wg.Wait()
	if got := c.Count("vm-shared", nil); got != 1000 {
		t.Fatalf("expected 1000 observations, got %d", got)
	}
}
