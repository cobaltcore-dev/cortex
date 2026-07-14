// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package clientcache

import (
	"context"
	"sync"
	"testing"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	toolscachek8s "k8s.io/client-go/tools/cache"
	ccache "sigs.k8s.io/controller-runtime/pkg/cache"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	"github.com/cobaltcore-dev/cortex/api/v1alpha1"
)

const azIndexField = "spec.availabilityZone"

func testScheme(t *testing.T) *runtime.Scheme {
	t.Helper()
	scheme := runtime.NewScheme()
	if err := v1alpha1.AddToScheme(scheme); err != nil {
		t.Fatalf("add to scheme: %v", err)
	}
	return scheme
}

func newReservation(name, az, rv string) *v1alpha1.Reservation {
	r := &v1alpha1.Reservation{
		ObjectMeta: metav1.ObjectMeta{
			Name:            name,
			UID:             types.UID("uid-" + name),
			ResourceVersion: rv,
		},
		Spec: v1alpha1.ReservationSpec{
			Type:             v1alpha1.ReservationTypeCommittedResource,
			AvailabilityZone: az,
		},
	}
	return r
}

func newTestClient(t *testing.T, objs ...client.Object) client.Client {
	t.Helper()
	return fake.NewClientBuilder().
		WithScheme(testScheme(t)).
		WithObjects(objs...).
		WithStatusSubresource(&v1alpha1.Reservation{}).
		WithIndex(&v1alpha1.Reservation{}, azIndexField, func(obj client.Object) []string {
			res, ok := obj.(*v1alpha1.Reservation)
			if !ok || res.Spec.AvailabilityZone == "" {
				return nil
			}
			return []string{res.Spec.AvailabilityZone}
		}).
		Build()
}

// fakeInformer is a controllable informer that records handlers and lets tests
// fire Add/Update events to trigger eviction.
type fakeInformer struct {
	ccache.Informer
	mu       sync.Mutex
	handlers []toolscachek8s.ResourceEventHandler
}

func (f *fakeInformer) AddEventHandler(h toolscachek8s.ResourceEventHandler) (toolscachek8s.ResourceEventHandlerRegistration, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.handlers = append(f.handlers, h)
	return nil, nil
}

func (f *fakeInformer) fireAdd(obj any) {
	f.mu.Lock()
	handlers := append([]toolscachek8s.ResourceEventHandler(nil), f.handlers...)
	f.mu.Unlock()
	for _, h := range handlers {
		h.OnAdd(obj, false)
	}
}

func (f *fakeInformer) fireUpdate(oldObj, newObj any) {
	f.mu.Lock()
	handlers := append([]toolscachek8s.ResourceEventHandler(nil), f.handlers...)
	f.mu.Unlock()
	for _, h := range handlers {
		h.OnUpdate(oldObj, newObj)
	}
}

// fakeInformerSource returns a single shared fakeInformer for all kinds.
type fakeInformerSource struct {
	inf *fakeInformer
}

func (s *fakeInformerSource) GetInformersForKind(ctx context.Context, obj client.Object) ([]ccache.Informer, error) {
	return []ccache.Informer{s.inf}, nil
}

func reservationConfig() Config {
	return Config{
		GVKs: []string{"cortex.cloud/v1alpha1/Reservation"},
		TTL:  metav1.Duration{Duration: 2 * time.Minute},
	}
}

func newCaching(t *testing.T, inner client.Client, src InformerSource) *CachingClient {
	t.Helper()
	c, err := New(inner, src, testScheme(t), reservationConfig())
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return c
}

func listReservations(t *testing.T, c client.Client, opts ...client.ListOption) []v1alpha1.Reservation {
	t.Helper()
	var list v1alpha1.ReservationList
	if err := c.List(context.Background(), &list, opts...); err != nil {
		t.Fatalf("List: %v", err)
	}
	return list.Items
}

// 1. Write/Read: Create then immediate List/Get shows the object despite an
// empty informer (fake inner client without the object pre-loaded... but the
// fake client persists creates, so we simulate informer lag by deleting from
// inner after caching — instead we verify overlay independently below).
func TestCreateThenGetVisible(t *testing.T) {
	inner := newTestClient(t)
	src := &fakeInformerSource{inf: &fakeInformer{}}
	c := newCaching(t, inner, src)

	r := newReservation("res-1", "az-1", "")
	if err := c.Create(context.Background(), r); err != nil {
		t.Fatalf("Create: %v", err)
	}

	var got v1alpha1.Reservation
	if err := c.Get(context.Background(), types.NamespacedName{Name: "res-1"}, &got); err != nil {
		t.Fatalf("Get after create: %v", err)
	}
	if got.Spec.AvailabilityZone != "az-1" {
		t.Fatalf("expected az-1, got %q", got.Spec.AvailabilityZone)
	}
}

// 2. Overlay with empty informer: inner List returns [], overlay entry still in
// the result.
func TestOverlayWhenInnerEmpty(t *testing.T) {
	inner := newTestClient(t) // no objects
	c := newCaching(t, inner, &fakeInformerSource{inf: &fakeInformer{}})

	// Directly seed the overlay to simulate a write whose object is not yet in
	// the (empty) inner client.
	c.overlay.upsert(reservationGVK(), newReservation("res-2", "az-1", "5"))

	items := listReservations(t, c)
	if len(items) != 1 || items[0].Name != "res-2" {
		t.Fatalf("expected overlay entry res-2, got %+v", items)
	}
}

// 3. Eviction: an informer sighting (Add or Update) evicts the overlay entry
// only when it matches by UID and carries a ResourceVersion >= the cached one.
func TestEviction(t *testing.T) {
	// Cached entry is always uid-res-3 @ RV 10.
	const cachedRV = "10"
	cases := []struct {
		name        string
		useUpdate   bool   // fire OnUpdate instead of OnAdd
		observedUID string // "" => reuse the cached object's UID
		observedRV  string
		wantEvicted bool
	}{
		{name: "add older RV keeps", observedRV: "9", wantEvicted: false},
		{name: "add equal RV evicts", observedRV: "10", wantEvicted: true},
		{name: "add newer RV evicts", observedRV: "11", wantEvicted: true},
		{name: "update newer RV evicts", useUpdate: true, observedRV: "11", wantEvicted: true},
		{name: "update older RV keeps", useUpdate: true, observedRV: "9", wantEvicted: false},
		{name: "uid mismatch keeps", observedUID: "uid-other", observedRV: "11", wantEvicted: false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			inner := newTestClient(t)
			inf := &fakeInformer{}
			c := newCaching(t, inner, &fakeInformerSource{inf: inf})

			ctx := t.Context()
			go func() {
				if err := c.Start(ctx); err != nil && ctx.Err() == nil {
					t.Errorf("c.Start: %v", err)
				}
			}()
			waitFor(t, func() bool {
				inf.mu.Lock()
				defer inf.mu.Unlock()
				return len(inf.handlers) > 0
			})

			c.overlay.upsert(reservationGVK(), newReservation("res-3", "az-1", cachedRV))

			observed := newReservation("res-3", "az-1", tc.observedRV)
			if tc.observedUID != "" {
				observed.UID = types.UID(tc.observedUID)
			}
			if tc.useUpdate {
				inf.fireUpdate(nil, observed)
			} else {
				inf.fireAdd(observed)
			}

			_, present := c.overlay.get(reservationGVK(), objectKey{name: "res-3"})
			if present == tc.wantEvicted {
				t.Fatalf("evicted=%v, want evicted=%v", !present, tc.wantEvicted)
			}
		})
	}
}

// TestEvictionIgnoresNonObject: an informer event carrying a non-client.Object
// payload is ignored and does not panic or evict.
func TestEvictionIgnoresNonObject(t *testing.T) {
	inner := newTestClient(t)
	inf := &fakeInformer{}
	c := newCaching(t, inner, &fakeInformerSource{inf: inf})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() {
		if err := c.Start(ctx); err != nil && ctx.Err() == nil {
			t.Errorf("c.Start: %v", err)
		}
	}()
	waitFor(t, func() bool {
		inf.mu.Lock()
		defer inf.mu.Unlock()
		return len(inf.handlers) > 0
	})

	c.overlay.upsert(reservationGVK(), newReservation("res-x", "az-1", "1"))
	inf.fireAdd("not-an-object")
	if _, ok := c.overlay.get(reservationGVK(), objectKey{name: "res-x"}); !ok {
		t.Fatalf("non-object event must not evict the entry")
	}
}

// 4. TTL: cleanupExpired removes expired entries.
func TestTTLCleanup(t *testing.T) {
	o := newOverlay(time.Minute)
	o.upsert(reservationGVK(), newReservation("res-4", "az-1", "1"))
	// Force expiry.
	o.mu.Lock()
	for _, entries := range o.byGVK {
		for _, e := range entries {
			e.expiresAt = time.Now().Add(-time.Second)
		}
	}
	o.mu.Unlock()
	o.cleanupExpired(time.Now())
	if _, ok := o.get(reservationGVK(), objectKey{name: "res-4"}); ok {
		t.Fatalf("expired entry should be removed")
	}
}

// 5. Tombstone: Delete filters the object from List/Get even though inner still
// has it.
func TestTombstone(t *testing.T) {
	r := newReservation("res-5", "az-1", "")
	inner := newTestClient(t, r)
	c := newCaching(t, inner, &fakeInformerSource{inf: &fakeInformer{}})

	if err := c.Delete(context.Background(), r); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	// Re-add to inner to simulate informer lag: fake client already removed it,
	// so re-create via inner directly (bypassing overlay).
	if err := inner.Create(context.Background(), newReservation("res-5", "az-1", "")); err != nil {
		t.Fatalf("re-create inner: %v", err)
	}

	var got v1alpha1.Reservation
	err := c.Get(context.Background(), types.NamespacedName{Name: "res-5"}, &got)
	if !apierrors.IsNotFound(err) {
		t.Fatalf("expected NotFound for tombstoned object, got %v", err)
	}
	items := listReservations(t, c)
	if len(items) != 0 {
		t.Fatalf("expected tombstone to filter from list, got %+v", items)
	}
}

// 6. Update/Patch: newer overlay version overrides stale inner read.
func TestUpdateOverridesInner(t *testing.T) {
	r := newReservation("res-6", "az-old", "1")
	inner := newTestClient(t, r)
	c := newCaching(t, inner, &fakeInformerSource{inf: &fakeInformer{}})

	// Read current to obtain the up-to-date ResourceVersion for update.
	var cur v1alpha1.Reservation
	if err := inner.Get(context.Background(), types.NamespacedName{Name: "res-6"}, &cur); err != nil {
		t.Fatalf("inner get: %v", err)
	}
	cur.Spec.AvailabilityZone = "az-new"
	if err := c.Update(context.Background(), &cur); err != nil {
		t.Fatalf("Update: %v", err)
	}

	var got v1alpha1.Reservation
	if err := c.Get(context.Background(), types.NamespacedName{Name: "res-6"}, &got); err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.Spec.AvailabilityZone != "az-new" {
		t.Fatalf("expected az-new from overlay, got %q", got.Spec.AvailabilityZone)
	}
}

// 7. Label-matching: overlay-only entry appears only for matching MatchingLabels.
func TestLabelMatching(t *testing.T) {
	inner := newTestClient(t)
	c := newCaching(t, inner, &fakeInformerSource{inf: &fakeInformer{}})

	r := newReservation("res-7", "az-1", "1")
	r.Labels = map[string]string{"team": "a"}
	c.overlay.upsert(reservationGVK(), r)

	match := listReservations(t, c, client.MatchingLabels{"team": "a"})
	if len(match) != 1 {
		t.Fatalf("expected match for team=a, got %+v", match)
	}
	noMatch := listReservations(t, c, client.MatchingLabels{"team": "b"})
	if len(noMatch) != 0 {
		t.Fatalf("expected no match for team=b, got %+v", noMatch)
	}
}

// 8. Field-matching: after IndexField registration, overlay-only entry appears
// only for matching MatchingFields.
func TestFieldMatching(t *testing.T) {
	inner := newTestClient(t)
	c := newCaching(t, inner, &fakeInformerSource{inf: &fakeInformer{}})

	if err := c.IndexField(context.Background(), &v1alpha1.Reservation{}, azIndexField, func(obj client.Object) []string {
		res := obj.(*v1alpha1.Reservation)
		if res.Spec.AvailabilityZone == "" {
			return nil
		}
		return []string{res.Spec.AvailabilityZone}
	}); err != nil {
		t.Fatalf("IndexField: %v", err)
	}

	c.overlay.upsert(reservationGVK(), newReservation("res-8", "az-1", "1"))

	match := listReservations(t, c, client.MatchingFields{azIndexField: "az-1"})
	if len(match) != 1 {
		t.Fatalf("expected field match az-1, got %+v", match)
	}
	noMatch := listReservations(t, c, client.MatchingFields{azIndexField: "az-2"})
	if len(noMatch) != 0 {
		t.Fatalf("expected no field match az-2, got %+v", noMatch)
	}
}

// 9. Non-cached GVK: calls pass through unchanged (no overlay effect).
func TestNonCachedGVKPassthrough(t *testing.T) {
	inner := newTestClient(t)
	// Config with no GVKs → Reservation is not cached.
	c, err := New(inner, &fakeInformerSource{inf: &fakeInformer{}}, testScheme(t), Config{})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	r := newReservation("res-9", "az-1", "")
	if err := c.Create(context.Background(), r); err != nil {
		t.Fatalf("Create: %v", err)
	}
	// Overlay must be empty for non-cached GVK.
	if _, ok := c.overlay.get(reservationGVK(), objectKey{name: "res-9"}); ok {
		t.Fatalf("non-cached GVK should not populate overlay")
	}
	// Delete it in inner, then Get should be NotFound (no overlay resurrection).
	if err := c.Delete(context.Background(), r); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	var got v1alpha1.Reservation
	if err := c.Get(context.Background(), types.NamespacedName{Name: "res-9"}, &got); !apierrors.IsNotFound(err) {
		t.Fatalf("expected NotFound, got %v", err)
	}
}

// 10. Dedup: object present in both informer (inner) and overlay appears once.
func TestDedup(t *testing.T) {
	r := newReservation("res-10", "az-1", "1")
	inner := newTestClient(t, r)
	c := newCaching(t, inner, &fakeInformerSource{inf: &fakeInformer{}})

	// Overlay holds a newer version of the same object.
	newer := newReservation("res-10", "az-1", "2")
	newer.Spec.TargetHost = "host-x"
	c.overlay.upsert(reservationGVK(), newer)

	items := listReservations(t, c)
	if len(items) != 1 {
		t.Fatalf("expected exactly one item after dedup, got %d: %+v", len(items), items)
	}
	if items[0].Spec.TargetHost != "host-x" {
		t.Fatalf("expected overlay version to win, got %+v", items[0])
	}
}

// helpers

func reservationGVK() schema.GroupVersionKind {
	return v1alpha1.GroupVersion.WithKind("Reservation")
}

func waitFor(t *testing.T, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("condition not met within timeout")
}
