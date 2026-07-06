// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package crdcache

import (
	"sync"
	"testing"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/cobaltcore-dev/cortex/api/v1alpha1"
)

func makeReservation(namespace, name string) *v1alpha1.Reservation {
	return &v1alpha1.Reservation{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: namespace,
			Name:      name,
		},
	}
}

func TestAssume_storesObject(t *testing.T) {
	c := New(Options{})
	r := makeReservation("ns", "res-1")
	c.Assume(r)

	got, ok := c.Get("ns/res-1")
	if !ok {
		t.Fatal("expected entry in cache, got miss")
	}
	if got.GetName() != "res-1" {
		t.Errorf("expected name res-1, got %s", got.GetName())
	}
}

func TestAssume_returnsDeepCopy(t *testing.T) {
	c := New(Options{})
	r := makeReservation("ns", "res-1")
	c.Assume(r)

	// Mutate the original — cache must not reflect the change.
	r.Name = "mutated"
	got, _ := c.Get("ns/res-1")
	if got.GetName() != "res-1" {
		t.Error("cache returned reference to original object, expected deep copy")
	}
}

func TestAssume_resetsTimer(t *testing.T) {
	ttl := 80 * time.Millisecond
	c := New(Options{Config: Config{CRDCache: CRDCacheSettings{TTL: Duration{ttl}}}})
	r := makeReservation("ns", "res-1")

	c.Assume(r)
	time.Sleep(50 * time.Millisecond)
	// Re-assume before TTL fires — timer should reset.
	c.Assume(r)
	time.Sleep(50 * time.Millisecond)
	// Should still be present (TTL hasn't elapsed since second Assume).
	if _, ok := c.Get("ns/res-1"); !ok {
		t.Error("entry evicted too early after timer reset")
	}
}

func TestForget_removesEntry(t *testing.T) {
	c := New(Options{})
	r := makeReservation("ns", "res-1")
	c.Assume(r)
	c.Forget("ns/res-1")

	if _, ok := c.Get("ns/res-1"); ok {
		t.Error("expected miss after Forget, got hit")
	}
}

func TestForget_noopOnMissing(t *testing.T) {
	c := New(Options{})
	// Should not panic.
	c.Forget("ns/does-not-exist")
}

func TestTTL_evictsAfterExpiry(t *testing.T) {
	c := New(Options{Config: Config{CRDCache: CRDCacheSettings{TTL: Duration{40 * time.Millisecond}}}})
	c.Assume(makeReservation("ns", "res-1"))
	time.Sleep(80 * time.Millisecond)

	if _, ok := c.Get("ns/res-1"); ok {
		t.Error("expected eviction by TTL, but entry is still present")
	}
}

func TestTTL_cancelledByForget(t *testing.T) {
	c := New(Options{Config: Config{CRDCache: CRDCacheSettings{TTL: Duration{50 * time.Millisecond}}}})
	c.Assume(makeReservation("ns", "res-1"))
	c.Forget("ns/res-1")

	// Wait for TTL to expire and confirm no panic/double-removal.
	time.Sleep(80 * time.Millisecond)
	if _, ok := c.Get("ns/res-1"); ok {
		t.Error("entry re-appeared after Forget + TTL")
	}
}

func TestList_returnsAllEntries(t *testing.T) {
	c := New(Options{})
	c.Assume(makeReservation("ns", "res-1"))
	c.Assume(makeReservation("ns", "res-2"))

	items := c.List()
	if len(items) != 2 {
		t.Errorf("expected 2 entries, got %d", len(items))
	}
}

func TestList_returnsDeepCopies(t *testing.T) {
	c := New(Options{})
	r := makeReservation("ns", "res-1")
	c.Assume(r)

	items := c.List()
	items[0].SetName("mutated")

	// Re-get from cache — should not reflect the mutation.
	got, _ := c.Get("ns/res-1")
	if got.GetName() != "res-1" {
		t.Error("List returned reference to internal entry, expected deep copy")
	}
}

func TestObjectKey_clusterScoped(t *testing.T) {
	r := makeReservation("", "global-res")
	key := ObjectKey(r)
	if key != "/global-res" {
		t.Errorf("unexpected key %q", key)
	}
}

func TestConcurrent_safeUnderRace(t *testing.T) {
	c := New(Options{Config: Config{CRDCache: CRDCacheSettings{TTL: Duration{20 * time.Millisecond}}}})
	var wg sync.WaitGroup
	for i := range 100 {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			r := makeReservation("ns", "res-concurrent")
			_ = i
			c.Assume(r)
			c.List()
			c.Get("ns/res-concurrent")
			c.Forget("ns/res-concurrent")
		}(i)
	}
	wg.Wait()
}
