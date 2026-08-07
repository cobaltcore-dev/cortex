// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package clientcache

import (
	"context"
	"errors"
	goruntime "runtime"
	"strconv"
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

// --- shared test infrastructure ---

func testScheme(t *testing.T) *runtime.Scheme {
	t.Helper()
	scheme := runtime.NewScheme()
	if err := v1alpha1.AddToScheme(scheme); err != nil {
		t.Fatalf("add to scheme: %v", err)
	}
	return scheme
}

func newReservation(name, az, rv string) *v1alpha1.Reservation {
	return &v1alpha1.Reservation{
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

// fakeClient composes a fake client.Client with a fakeInformer to satisfy the
// clientcache.Client interface.
type fakeClient struct {
	client.Client
	inf *fakeInformer
}

func (f *fakeClient) GetInformersForKind(_ context.Context, _ client.Object) ([]ccache.Informer, error) {
	return []ccache.Informer{f.inf}, nil
}

func newTestClient(t *testing.T, objs ...client.Object) *fakeClient {
	t.Helper()
	return &fakeClient{
		Client: fake.NewClientBuilder().
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
			Build(),
		inf: &fakeInformer{},
	}
}

// errClient wraps a Client and injects configurable errors into mutating/read
// operations, so the error-propagation paths of CachingClient (which must not
// touch the overlay on failure) can be exercised.
type errClient struct {
	Client
	createErr       error
	updateErr       error
	patchErr        error
	deleteErr       error
	deleteAllOfErr  error
	getErr          error
	listErr         error
}

func (e *errClient) Create(ctx context.Context, obj client.Object, opts ...client.CreateOption) error {
	if e.createErr != nil {
		return e.createErr
	}
	return e.Client.Create(ctx, obj, opts...)
}

func (e *errClient) Update(ctx context.Context, obj client.Object, opts ...client.UpdateOption) error {
	if e.updateErr != nil {
		return e.updateErr
	}
	return e.Client.Update(ctx, obj, opts...)
}

func (e *errClient) Patch(ctx context.Context, obj client.Object, patch client.Patch, opts ...client.PatchOption) error {
	if e.patchErr != nil {
		return e.patchErr
	}
	return e.Client.Patch(ctx, obj, patch, opts...)
}

func (e *errClient) Delete(ctx context.Context, obj client.Object, opts ...client.DeleteOption) error {
	if e.deleteErr != nil {
		return e.deleteErr
	}
	return e.Client.Delete(ctx, obj, opts...)
}

func (e *errClient) DeleteAllOf(ctx context.Context, obj client.Object, opts ...client.DeleteAllOfOption) error {
	if e.deleteAllOfErr != nil {
		return e.deleteAllOfErr
	}
	return e.Client.DeleteAllOf(ctx, obj, opts...)
}

func (e *errClient) Get(ctx context.Context, key client.ObjectKey, obj client.Object, opts ...client.GetOption) error {
	if e.getErr != nil {
		return e.getErr
	}
	return e.Client.Get(ctx, key, obj, opts...)
}

func (e *errClient) List(ctx context.Context, list client.ObjectList, opts ...client.ListOption) error {
	if e.listErr != nil {
		return e.listErr
	}
	return e.Client.List(ctx, list, opts...)
}

// forceInnerAZ writes a divergent AvailabilityZone directly to the inner
// client, bypassing the caching wrapper. Used to prove reads are served from
// the overlay, not the inner client.
func forceInnerAZ(t *testing.T, inner client.Client, name, az string) {
	t.Helper()
	var cur v1alpha1.Reservation
	if err := inner.Get(context.Background(), types.NamespacedName{Name: name}, &cur); err != nil {
		t.Fatalf("forceInnerAZ get: %v", err)
	}
	cur.Spec.AvailabilityZone = az
	if err := inner.Update(context.Background(), &cur); err != nil {
		t.Fatalf("forceInnerAZ update: %v", err)
	}
}

// forceInnerStatusHost writes a divergent status Host directly to the inner client.
func forceInnerStatusHost(t *testing.T, inner client.Client, name, host string) {
	t.Helper()
	var cur v1alpha1.Reservation
	if err := inner.Get(context.Background(), types.NamespacedName{Name: name}, &cur); err != nil {
		t.Fatalf("forceInnerStatusHost get: %v", err)
	}
	cur.Status.Host = host
	if err := inner.Status().Update(context.Background(), &cur); err != nil {
		t.Fatalf("forceInnerStatusHost update: %v", err)
	}
}

// unknownObject is a client.Object whose type is not registered in the test
// scheme, used to exercise the "unresolvable GVK" branches.
type unknownObject struct {
	metav1.TypeMeta
	metav1.ObjectMeta
}

func (u *unknownObject) DeepCopyObject() runtime.Object { return u }

func reservationConfig() Config {
	return Config{
		GVKs: []string{"cortex.cloud/v1alpha1/Reservation"},
		TTL:  metav1.Duration{Duration: 2 * time.Minute},
	}
}

func newCaching(t *testing.T, inner Client) *CachingClient {
	t.Helper()
	c, err := New(inner, testScheme(t), reservationConfig())
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

// --- constructor tests ---

func TestNewUnknownGVKError(t *testing.T) {
	_, err := New(newTestClient(t), testScheme(t), Config{
		GVKs: []string{"cortex.cloud/v1alpha1/DoesNotExist"},
	})
	if err == nil {
		t.Fatalf("expected error for unknown GVK, got nil")
	}
}

func TestNewDefaultTTL(t *testing.T) {
	c, err := New(newTestClient(t), testScheme(t), Config{
		GVKs: []string{"cortex.cloud/v1alpha1/Reservation"},
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if c.ttl != defaultTTL {
		t.Fatalf("expected ttl %v, got %v", defaultTTL, c.ttl)
	}
}

func TestNewExplicitTTL(t *testing.T) {
	c, err := New(newTestClient(t), testScheme(t), Config{
		GVKs: []string{"cortex.cloud/v1alpha1/Reservation"},
		TTL:  metav1.Duration{Duration: 90 * time.Second},
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if c.ttl != 90*time.Second {
		t.Fatalf("expected ttl 90s, got %v", c.ttl)
	}
}

// --- overlay behaviour tests ---

func TestCreateThenGetVisible(t *testing.T) {
	c := newCaching(t, newTestClient(t))

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

func TestOverlayWhenInnerEmpty(t *testing.T) {
	c := newCaching(t, newTestClient(t))
	c.upsert(reservationGVK(), newReservation("res-2", "az-1", "5"))

	if items := listReservations(t, c); len(items) != 1 || items[0].Name != "res-2" {
		t.Fatalf("expected overlay entry res-2, got %+v", items)
	}
}

func TestEviction(t *testing.T) {
	const cachedRV = "10"
	cases := []struct {
		name        string
		useUpdate   bool
		observedUID string
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
			c := newCaching(t, inner)

			ctx := t.Context()
			go func() {
				if err := c.Start(ctx); err != nil && ctx.Err() == nil {
					t.Errorf("c.Start: %v", err)
				}
			}()
			waitFor(t, func() bool {
				inner.inf.mu.Lock()
				defer inner.inf.mu.Unlock()
				return len(inner.inf.handlers) > 0
			})

			c.upsert(reservationGVK(), newReservation("res-3", "az-1", cachedRV))

			observed := newReservation("res-3", "az-1", tc.observedRV)
			if tc.observedUID != "" {
				observed.UID = types.UID(tc.observedUID)
			}
			if tc.useUpdate {
				inner.inf.fireUpdate(nil, observed)
			} else {
				inner.inf.fireAdd(observed)
			}

			_, present := c.getEntry(reservationGVK(), objectKey{name: "res-3"})
			if present == tc.wantEvicted {
				t.Fatalf("evicted=%v, want evicted=%v", !present, tc.wantEvicted)
			}
		})
	}
}

func TestEvictionIgnoresNonObject(t *testing.T) {
	inner := newTestClient(t)
	c := newCaching(t, inner)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() {
		if err := c.Start(ctx); err != nil && ctx.Err() == nil {
			t.Errorf("c.Start: %v", err)
		}
	}()
	waitFor(t, func() bool {
		inner.inf.mu.Lock()
		defer inner.inf.mu.Unlock()
		return len(inner.inf.handlers) > 0
	})

	c.upsert(reservationGVK(), newReservation("res-x", "az-1", "1"))
	inner.inf.fireAdd("not-an-object")
	if _, ok := c.getEntry(reservationGVK(), objectKey{name: "res-x"}); !ok {
		t.Fatalf("non-object event must not evict the entry")
	}
}

func TestTTLCleanup(t *testing.T) {
	c := newCaching(t, newTestClient(t))
	c.upsert(reservationGVK(), newReservation("res-4", "az-1", "1"))
	c.mu.Lock()
	for _, entries := range c.byGVK {
		for _, e := range entries {
			e.expiresAt = time.Now().Add(-time.Second)
		}
	}
	c.mu.Unlock()
	c.cleanupExpired(time.Now())
	if _, ok := c.getEntry(reservationGVK(), objectKey{name: "res-4"}); ok {
		t.Fatalf("expired entry should be removed")
	}
}

func TestTombstone(t *testing.T) {
	r := newReservation("res-5", "az-1", "")
	inner := newTestClient(t, r)
	c := newCaching(t, inner)

	if err := c.Delete(context.Background(), r); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	// Re-create directly in inner to simulate informer lag.
	if err := inner.Create(context.Background(), newReservation("res-5", "az-1", "")); err != nil {
		t.Fatalf("re-create inner: %v", err)
	}

	var got v1alpha1.Reservation
	if err := c.Get(context.Background(), types.NamespacedName{Name: "res-5"}, &got); !apierrors.IsNotFound(err) {
		t.Fatalf("expected NotFound for tombstoned object, got %v", err)
	}
	if items := listReservations(t, c); len(items) != 0 {
		t.Fatalf("expected tombstone to filter from list, got %+v", items)
	}
}

func TestDeleteAllOf(t *testing.T) {
	r1 := newReservation("res-dao-1", "az-1", "1")
	r1.Labels = map[string]string{"zone": "a"}
	r2 := newReservation("res-dao-2", "az-2", "1")
	r2.Labels = map[string]string{"zone": "b"}
	inner := newTestClient(t, r1, r2)
	c := newCaching(t, inner)

	// Populate overlay for both so we can verify tombstoning.
	c.upsert(reservationGVK(), r1)
	c.upsert(reservationGVK(), r2)

	// DeleteAllOf with a label selector — only r1 should be tombstoned.
	if err := c.DeleteAllOf(context.Background(), &v1alpha1.Reservation{}, client.MatchingLabels{"zone": "a"}); err != nil {
		t.Fatalf("DeleteAllOf: %v", err)
	}

	var got v1alpha1.Reservation
	if err := c.Get(context.Background(), types.NamespacedName{Name: "res-dao-1"}, &got); !apierrors.IsNotFound(err) {
		t.Fatalf("expected NotFound for tombstoned res-dao-1, got %v", err)
	}
	if err := c.Get(context.Background(), types.NamespacedName{Name: "res-dao-2"}, &got); err != nil {
		t.Fatalf("res-dao-2 should still be visible, got %v", err)
	}
}

func TestUpdateOverridesInner(t *testing.T) {
	r := newReservation("res-6", "az-old", "1")
	inner := newTestClient(t, r)
	c := newCaching(t, inner)

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

func TestLabelMatching(t *testing.T) {
	c := newCaching(t, newTestClient(t))
	r := newReservation("res-7", "az-1", "1")
	r.Labels = map[string]string{"team": "a"}
	c.upsert(reservationGVK(), r)

	if match := listReservations(t, c, client.MatchingLabels{"team": "a"}); len(match) != 1 {
		t.Fatalf("expected match for team=a, got %+v", match)
	}
	if noMatch := listReservations(t, c, client.MatchingLabels{"team": "b"}); len(noMatch) != 0 {
		t.Fatalf("expected no match for team=b, got %+v", noMatch)
	}
}

func TestFieldMatching(t *testing.T) {
	c := newCaching(t, newTestClient(t))
	if err := c.IndexField(context.Background(), &v1alpha1.Reservation{}, azIndexField, func(obj client.Object) []string {
		res := obj.(*v1alpha1.Reservation)
		if res.Spec.AvailabilityZone == "" {
			return nil
		}
		return []string{res.Spec.AvailabilityZone}
	}); err != nil {
		t.Fatalf("IndexField: %v", err)
	}
	c.upsert(reservationGVK(), newReservation("res-8", "az-1", "1"))

	if match := listReservations(t, c, client.MatchingFields{azIndexField: "az-1"}); len(match) != 1 {
		t.Fatalf("expected field match az-1, got %+v", match)
	}
	if noMatch := listReservations(t, c, client.MatchingFields{azIndexField: "az-2"}); len(noMatch) != 0 {
		t.Fatalf("expected no field match az-2, got %+v", noMatch)
	}
}

func TestNonCachedGVKPassthrough(t *testing.T) {
	inner := newTestClient(t)
	c, err := New(inner, testScheme(t), Config{})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	r := newReservation("res-9", "az-1", "")
	if err := c.Create(context.Background(), r); err != nil {
		t.Fatalf("Create: %v", err)
	}
	if _, ok := c.getEntry(reservationGVK(), objectKey{name: "res-9"}); ok {
		t.Fatalf("non-cached GVK should not populate overlay")
	}
	if err := c.Delete(context.Background(), r); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	var got v1alpha1.Reservation
	if err := c.Get(context.Background(), types.NamespacedName{Name: "res-9"}, &got); !apierrors.IsNotFound(err) {
		t.Fatalf("expected NotFound, got %v", err)
	}
}

func TestDedup(t *testing.T) {
	r := newReservation("res-10", "az-1", "1")
	c := newCaching(t, newTestClient(t, r))

	newer := newReservation("res-10", "az-1", "2")
	newer.Spec.TargetHost = "host-x"
	c.upsert(reservationGVK(), newer)

	items := listReservations(t, c)
	if len(items) != 1 {
		t.Fatalf("expected exactly one item after dedup, got %d: %+v", len(items), items)
	}
	if items[0].Spec.TargetHost != "host-x" {
		t.Fatalf("expected overlay version to win, got %+v", items[0])
	}
}

// --- error-propagation tests ---

func TestWriteErrorLeavesOverlayUntouched(t *testing.T) {
	sentinel := errors.New("boom")
	cases := []struct {
		name     string
		rv       string
		seed     bool
		mkClient func(inner *fakeClient) Client
		op       func(c *CachingClient, r *v1alpha1.Reservation) error
	}{
		{
			name:     "create",
			mkClient: func(inner *fakeClient) Client { return &errClient{Client: inner, createErr: sentinel} },
			op:       func(c *CachingClient, r *v1alpha1.Reservation) error { return c.Create(context.Background(), r) },
		},
		{
			name:     "update",
			rv:       "1",
			mkClient: func(inner *fakeClient) Client { return &errClient{Client: inner, updateErr: sentinel} },
			op:       func(c *CachingClient, r *v1alpha1.Reservation) error { return c.Update(context.Background(), r) },
		},
		{
			name:     "patch",
			rv:       "1",
			mkClient: func(inner *fakeClient) Client { return &errClient{Client: inner, patchErr: sentinel} },
			op: func(c *CachingClient, r *v1alpha1.Reservation) error {
				p := r.DeepCopy()
				p.Spec.AvailabilityZone = "az-new"
				return c.Patch(context.Background(), p, client.MergeFrom(r))
			},
		},
		{
			name:     "delete",
			seed:     true,
			mkClient: func(inner *fakeClient) Client { return &errClient{Client: inner, deleteErr: sentinel} },
			op:       func(c *CachingClient, r *v1alpha1.Reservation) error { return c.Delete(context.Background(), r) },
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := newReservation("res-"+tc.name, "az-1", tc.rv)
			var base *fakeClient
			if tc.seed {
				base = newTestClient(t, r)
			} else {
				base = newTestClient(t)
			}
			c := newCaching(t, tc.mkClient(base))

			if err := tc.op(c, r); !errors.Is(err, sentinel) {
				t.Fatalf("expected sentinel error, got %v", err)
			}
			if _, ok := c.getEntry(reservationGVK(), objectKey{name: r.Name}); ok {
				t.Fatalf("overlay must not be touched on %s failure", tc.name)
			}
		})
	}
}

func TestWriteServedFromOverlay(t *testing.T) {
	cases := []struct {
		name    string
		write   func(t *testing.T, c *CachingClient, cur *v1alpha1.Reservation) string
		diverge func(t *testing.T, inner client.Client, name string)
		read    func(*v1alpha1.Reservation) string
	}{
		{
			name: "patch spec",
			write: func(t *testing.T, c *CachingClient, cur *v1alpha1.Reservation) string {
				base := cur.DeepCopy()
				cur.Spec.AvailabilityZone = "az-new"
				if err := c.Patch(context.Background(), cur, client.MergeFrom(base)); err != nil {
					t.Fatalf("Patch: %v", err)
				}
				return "az-new"
			},
			diverge: func(t *testing.T, inner client.Client, name string) { forceInnerAZ(t, inner, name, "az-stale") },
			read:    func(r *v1alpha1.Reservation) string { return r.Spec.AvailabilityZone },
		},
		{
			name: "status update",
			write: func(t *testing.T, c *CachingClient, cur *v1alpha1.Reservation) string {
				cur.Status.Host = "host-active"
				if err := c.Status().Update(context.Background(), cur); err != nil {
					t.Fatalf("Status().Update: %v", err)
				}
				return "host-active"
			},
			diverge: func(t *testing.T, inner client.Client, name string) {
				forceInnerStatusHost(t, inner, name, "host-stale")
			},
			read: func(r *v1alpha1.Reservation) string { return r.Status.Host },
		},
		{
			name: "status patch",
			write: func(t *testing.T, c *CachingClient, cur *v1alpha1.Reservation) string {
				base := cur.DeepCopy()
				cur.Status.Host = "host-patched"
				if err := c.Status().Patch(context.Background(), cur, client.MergeFrom(base)); err != nil {
					t.Fatalf("Status().Patch: %v", err)
				}
				return "host-patched"
			},
			diverge: func(t *testing.T, inner client.Client, name string) {
				forceInnerStatusHost(t, inner, name, "host-stale")
			},
			read: func(r *v1alpha1.Reservation) string { return r.Status.Host },
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := newReservation("res-served", "az-1", "")
			inner := newTestClient(t, r)
			c := newCaching(t, inner)

			var cur v1alpha1.Reservation
			if err := inner.Get(context.Background(), types.NamespacedName{Name: r.Name}, &cur); err != nil {
				t.Fatalf("inner get: %v", err)
			}
			want := tc.write(t, c, &cur)
			tc.diverge(t, inner, r.Name)

			var got v1alpha1.Reservation
			if err := c.Get(context.Background(), types.NamespacedName{Name: r.Name}, &got); err != nil {
				t.Fatalf("Get: %v", err)
			}
			if tc.read(&got) != want {
				t.Fatalf("expected overlay value %q to be served, got %q", want, tc.read(&got))
			}
		})
	}
}

func TestGetPropagatesNonNotFoundError(t *testing.T) {
	sentinel := errors.New("get boom")
	c := newCaching(t, &errClient{Client: newTestClient(t), getErr: sentinel})
	c.upsert(reservationGVK(), newReservation("res-ge", "az-1", "1"))

	var got v1alpha1.Reservation
	if err := c.Get(context.Background(), types.NamespacedName{Name: "res-ge"}, &got); !errors.Is(err, sentinel) {
		t.Fatalf("expected sentinel error, got %v", err)
	}
}

func TestGetOverlayResurrectsNotFound(t *testing.T) {
	c := newCaching(t, newTestClient(t))
	c.upsert(reservationGVK(), newReservation("res-gr", "az-z", "1"))

	var got v1alpha1.Reservation
	if err := c.Get(context.Background(), types.NamespacedName{Name: "res-gr"}, &got); err != nil {
		t.Fatalf("expected overlay to satisfy Get, got %v", err)
	}
	if got.Spec.AvailabilityZone != "az-z" {
		t.Fatalf("expected az-z from overlay, got %q", got.Spec.AvailabilityZone)
	}
}

func TestGetNotFoundWithNoOverlay(t *testing.T) {
	c := newCaching(t, newTestClient(t))

	var got v1alpha1.Reservation
	if err := c.Get(context.Background(), types.NamespacedName{Name: "missing"}, &got); !apierrors.IsNotFound(err) {
		t.Fatalf("expected NotFound, got %v", err)
	}
}

func TestGetNonCachedPropagatesError(t *testing.T) {
	sentinel := errors.New("get boom")
	c, err := New(&errClient{Client: newTestClient(t), getErr: sentinel}, testScheme(t), Config{})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	var got v1alpha1.Reservation
	if gerr := c.Get(context.Background(), types.NamespacedName{Name: "x"}, &got); !errors.Is(gerr, sentinel) {
		t.Fatalf("expected sentinel error, got %v", gerr)
	}
}

func TestListPropagatesError(t *testing.T) {
	sentinel := errors.New("list boom")
	c := newCaching(t, &errClient{Client: newTestClient(t), listErr: sentinel})
	c.upsert(reservationGVK(), newReservation("res-le", "az-1", "1"))

	var list v1alpha1.ReservationList
	if err := c.List(context.Background(), &list); !errors.Is(err, sentinel) {
		t.Fatalf("expected sentinel error, got %v", err)
	}
}

func TestStatusUpdateErrorLeavesOverlayUntouched(t *testing.T) {
	c := newCaching(t, newTestClient(t))

	r := newReservation("res-se", "az-1", "1")
	if err := c.Status().Update(context.Background(), r); err == nil {
		t.Fatalf("expected status update to fail for missing object")
	}
	if _, ok := c.getEntry(reservationGVK(), objectKey{name: "res-se"}); ok {
		t.Fatalf("overlay must not be populated on status update failure")
	}
}

func TestStatusCreateDelegates(t *testing.T) {
	r := newReservation("res-sc", "az-1", "")
	c := newCaching(t, newTestClient(t, r))

	if err := c.Status().Create(context.Background(), r, r); err == nil {
		t.Fatalf("expected Status().Create to fail on fake client")
	}
	if _, ok := c.getEntry(reservationGVK(), objectKey{name: "res-sc"}); ok {
		t.Fatalf("Status().Create must not populate the overlay")
	}
}

func TestStatusUpdateNonCachedNoOverlay(t *testing.T) {
	r := newReservation("res-sn", "az-1", "")
	inner := newTestClient(t, r)
	c, err := New(inner, testScheme(t), Config{})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	var cur v1alpha1.Reservation
	if err := inner.Get(context.Background(), types.NamespacedName{Name: "res-sn"}, &cur); err != nil {
		t.Fatalf("inner get: %v", err)
	}
	cur.Status.Host = "host-active"
	if err := c.Status().Update(context.Background(), &cur); err != nil {
		t.Fatalf("Status().Update: %v", err)
	}
	if _, ok := c.getEntry(reservationGVK(), objectKey{name: "res-sn"}); ok {
		t.Fatalf("non-cached GVK status update should not populate overlay")
	}
}

// --- concurrency regression test ---

// orderingClient is a fake inner client whose Update records the committed
// ResourceVersion (in inner-commit order) and then yields, widening the window
// between the inner commit and the overlay upsert. This exposes the ordering
// race the per-object write lock is meant to prevent: without the lock two
// concurrent writers to the same object can commit in one order yet upsert in
// the reverse order, leaving the overlay behind the apiserver.
//
// The per-object write lock makes each inner call + upsert atomic, so the inner
// commit order and the overlay upsert order are identical: the overlay always
// reflects the last write that reached the inner client (lastRV).
type orderingClient struct {
	Client
	mu     sync.Mutex
	lastRV string // ResourceVersion of the most recent inner commit
}

func (o *orderingClient) Update(ctx context.Context, obj client.Object, opts ...client.UpdateOption) error {
	o.mu.Lock()
	o.lastRV = obj.GetResourceVersion()
	o.mu.Unlock()
	// Yield after the inner commit but before the caller upserts, widening the
	// commit-vs-overlay reorder window.
	goruntime.Gosched()
	return nil
}

// TestConcurrentUpdatesOverlayNotBehind fires many concurrent Updates to the
// SAME object with distinct ResourceVersions. The per-object write lock must
// make each inner call + overlay update atomic, so once all writes settle the
// overlay entry reflects the last write that reached the inner client (overlay
// RV == last committed RV, never a reordered/stale one). Run under -race to
// also catch data races.
func TestConcurrentUpdatesOverlayNotBehind(t *testing.T) {
	const (
		rounds = 50
		n      = 8
	)
	for round := range rounds {
		oc := &orderingClient{Client: newTestClient(t)}
		c := newCaching(t, oc)

		var wg sync.WaitGroup
		for i := 1; i <= n; i++ {
			wg.Add(1)
			go func(rv int) {
				defer wg.Done()
				r := newReservation("res-conc", "az-1", strconv.Itoa(rv))
				if err := c.Update(context.Background(), r); err != nil {
					t.Errorf("Update rv=%d: %v", rv, err)
				}
			}(i)
		}
		wg.Wait()

		oc.mu.Lock()
		lastRV := oc.lastRV
		oc.mu.Unlock()

		e, ok := c.getEntry(reservationGVK(), objectKey{name: "res-conc"})
		if !ok {
			t.Fatalf("round %d: expected overlay entry for res-conc", round)
		}
		if e.resourceVersion != lastRV {
			t.Fatalf("round %d: overlay RV %q does not match last inner commit %q",
				round, e.resourceVersion, lastRV)
		}
	}
}

// --- helper / utility tests ---

func TestGVKForUnknownType(t *testing.T) {
	c := newCaching(t, newTestClient(t))
	if _, cached := c.gvkFor(&unknownObject{}); cached {
		t.Fatalf("unknown type must not be reported as cached")
	}
}

func TestTrimListSuffix(t *testing.T) {
	cases := []struct {
		in       string
		wantKind string
		wantOK   bool
	}{
		{"ReservationList", "Reservation", true},
		{"List", "List", false},
		{"Reservation", "Reservation", false},
		{"", "", false},
	}
	for _, tc := range cases {
		gotKind, gotOK := trimListSuffix(tc.in)
		if gotKind != tc.wantKind || gotOK != tc.wantOK {
			t.Errorf("trimListSuffix(%q) = (%q, %v), want (%q, %v)", tc.in, gotKind, gotOK, tc.wantKind, tc.wantOK)
		}
	}
}
