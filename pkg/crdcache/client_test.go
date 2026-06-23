// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package crdcache

import (
	"context"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	"github.com/cobaltcore-dev/cortex/api/v1alpha1"
)

func newScheme(t *testing.T) *runtime.Scheme {
	t.Helper()
	s := runtime.NewScheme()
	if err := v1alpha1.AddToScheme(s); err != nil {
		t.Fatalf("add scheme: %v", err)
	}
	return s
}

func newCachingClient(t *testing.T, existing ...client.Object) (*CachingClient, *CRDCache) {
	t.Helper()
	inner := fake.NewClientBuilder().
		WithScheme(newScheme(t)).
		WithObjects(existing...).
		Build()
	cache := New(Options{
		ShouldCache: func(obj client.Object) bool {
			_, ok := obj.(*v1alpha1.Reservation)
			return ok
		},
	})
	return NewCachingClient(inner, cache, Options{}), cache
}

func res(namespace, name string, lbls map[string]string) *v1alpha1.Reservation {
	return &v1alpha1.Reservation{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: namespace,
			Name:      name,
			Labels:    lbls,
		},
	}
}

func TestCreate_populatesCache(t *testing.T) {
	cc, cache := newCachingClient(t)
	ctx := context.Background()

	r := res("ns", "new-res", nil)
	if err := cc.Create(ctx, r); err != nil {
		t.Fatalf("Create: %v", err)
	}

	// Cache should now hold the entry.
	if _, ok := cache.Get("ns/new-res"); !ok {
		t.Error("expected entry in cache after Create")
	}
}

func TestList_overlay_cachedAbsentFromInformer(t *testing.T) {
	// inner client has no Reservations; cache has one.
	cc, cache := newCachingClient(t)
	ctx := context.Background()

	assumed := res("ns", "assumed-res", nil)
	cache.Assume(assumed)

	var list v1alpha1.ReservationList
	if err := cc.List(ctx, &list); err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(list.Items) != 1 {
		t.Errorf("expected 1 item (from cache), got %d", len(list.Items))
	}
	if list.Items[0].Name != "assumed-res" {
		t.Errorf("unexpected item name: %s", list.Items[0].Name)
	}
}

func TestList_overlay_cachedPresentInInformer(t *testing.T) {
	// inner client has the same object; cache also has it — should appear exactly once.
	existing := res("ns", "shared-res", nil)
	cc, cache := newCachingClient(t, existing)
	ctx := context.Background()

	cache.Assume(existing)

	var list v1alpha1.ReservationList
	if err := cc.List(ctx, &list); err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(list.Items) != 1 {
		t.Errorf("expected exactly 1 item, got %d", len(list.Items))
	}
}

func TestList_overlay_labelFilter(t *testing.T) {
	cc, cache := newCachingClient(t)
	ctx := context.Background()

	cache.Assume(res("ns", "match", map[string]string{"type": "inflight"}))
	cache.Assume(res("ns", "no-match", map[string]string{"type": "committed"}))

	var list v1alpha1.ReservationList
	if err := cc.List(ctx, &list, client.MatchingLabels{"type": "inflight"}); err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(list.Items) != 1 {
		t.Errorf("expected 1 label-matching item, got %d", len(list.Items))
	}
	if list.Items[0].Name != "match" {
		t.Errorf("wrong item returned: %s", list.Items[0].Name)
	}
}

func TestList_overlay_namespaceFilter(t *testing.T) {
	cc, cache := newCachingClient(t)
	ctx := context.Background()

	cache.Assume(res("ns-a", "res-a", nil))
	cache.Assume(res("ns-b", "res-b", nil))

	var list v1alpha1.ReservationList
	if err := cc.List(ctx, &list, client.InNamespace("ns-a")); err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(list.Items) != 1 {
		t.Errorf("expected 1 namespace-matching item, got %d", len(list.Items))
	}
	if list.Items[0].Namespace != "ns-a" {
		t.Errorf("wrong namespace: %s", list.Items[0].Namespace)
	}
}

func TestGet_hitsCache(t *testing.T) {
	// Object in cache, NOT in inner client.
	cc, cache := newCachingClient(t)
	ctx := context.Background()

	cache.Assume(res("ns", "cache-only", nil))

	var got v1alpha1.Reservation
	if err := cc.Get(ctx, client.ObjectKey{Namespace: "ns", Name: "cache-only"}, &got); err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.Name != "cache-only" {
		t.Errorf("unexpected name: %s", got.Name)
	}
}

func TestGet_fallsThrough(t *testing.T) {
	// Object only in inner client (fake), not in cache.
	existing := res("ns", "client-only", nil)
	cc, _ := newCachingClient(t, existing)
	ctx := context.Background()

	var got v1alpha1.Reservation
	if err := cc.Get(ctx, client.ObjectKey{Namespace: "ns", Name: "client-only"}, &got); err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.Name != "client-only" {
		t.Errorf("unexpected name: %s", got.Name)
	}
}

func TestUpdate_updatesCacheEntry(t *testing.T) {
	cc, cache := newCachingClient(t)
	ctx := context.Background()

	// Create via caching client so the object has a ResourceVersion from the fake server.
	r := res("ns", "res-1", nil)
	if err := cc.Create(ctx, r); err != nil {
		t.Fatalf("Create: %v", err)
	}

	// Update with a new label (r now has ResourceVersion set by Create).
	r.Labels = map[string]string{"updated": "true"}
	if err := cc.Update(ctx, r); err != nil {
		t.Fatalf("Update: %v", err)
	}

	// Cache should reflect the update.
	got, ok := cache.Get("ns/res-1")
	if !ok {
		t.Fatal("expected entry in cache after Update")
	}
	if got.GetLabels()["updated"] != "true" {
		t.Error("cache entry not updated after Update")
	}
}

func TestDelete_removesFromCache(t *testing.T) {
	existing := res("ns", "res-1", nil)
	cc, cache := newCachingClient(t, existing)
	ctx := context.Background()

	cache.Assume(existing)

	if err := cc.Delete(ctx, existing); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if _, ok := cache.Get("ns/res-1"); ok {
		t.Error("expected cache miss after Delete")
	}
}

func TestEviction_informerFires(t *testing.T) {
	cc, cache := newCachingClient(t)
	ctx := context.Background()

	assumed := res("ns", "evict-me", nil)
	cache.Assume(assumed)

	// Simulate informer seeing the object by calling Forget directly.
	cache.Forget(ObjectKey(assumed))

	var list v1alpha1.ReservationList
	if err := cc.List(ctx, &list); err != nil {
		t.Fatalf("List: %v", err)
	}
	for _, item := range list.Items {
		if item.Name == "evict-me" {
			t.Error("evicted object should not appear in List result")
		}
	}
}

func TestShouldCache_respectsFilter(t *testing.T) {
	// Build a CachingClient that caches nothing (ShouldCache always false).
	inner := fake.NewClientBuilder().WithScheme(newScheme(t)).Build()
	cache := New(Options{})
	cc := NewCachingClient(inner, cache, Options{
		ShouldCache: func(client.Object) bool { return false },
	})
	// Override the embedded client's opts via the CachingClient's opts field.
	cc.opts.ShouldCache = func(client.Object) bool { return false }

	ctx := context.Background()
	r := res("ns", "no-cache", nil)
	if err := cc.Create(ctx, r); err != nil {
		t.Fatalf("Create: %v", err)
	}
	if _, ok := cache.Get("ns/no-cache"); ok {
		t.Error("object should not have been cached when ShouldCache returns false")
	}
}
