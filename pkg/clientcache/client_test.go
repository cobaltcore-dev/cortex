// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package clientcache

import (
	"context"
	"errors"
	"testing"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/cobaltcore-dev/cortex/api/v1alpha1"
)

// errClient wraps an inner client.Client and injects a configurable error into
// each mutating/read operation, so the error-propagation paths of
// CachingClient (which must not touch the overlay on failure) can be exercised.
type errClient struct {
	client.Client
	createErr error
	updateErr error
	patchErr  error
	deleteErr error
	getErr    error
	listErr   error
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

// forceInnerAZ writes a divergent AvailabilityZone directly to the inner client,
// bypassing the caching wrapper (and its overlay). Used to prove that reads
// through the caching client are served from the overlay, not the inner client.
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

// forceInnerStatusHost writes a divergent status Host directly to the inner
// client, bypassing the caching wrapper.
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

// TestNewUnknownGVKError: New fails when a configured GVK string is not
// registered in the scheme.
func TestNewUnknownGVKError(t *testing.T) {
	inner := newTestClient(t)
	_, err := New(inner, &fakeInformerSource{inf: &fakeInformer{}}, testScheme(t), Config{
		GVKs: []string{"cortex.cloud/v1alpha1/DoesNotExist"},
	})
	if err == nil {
		t.Fatalf("expected error for unknown GVK, got nil")
	}
}

// TestNewDefaultTTL: a zero TTL in the config falls back to defaultTTL.
func TestNewDefaultTTL(t *testing.T) {
	inner := newTestClient(t)
	c, err := New(inner, &fakeInformerSource{inf: &fakeInformer{}}, testScheme(t), Config{
		GVKs: []string{"cortex.cloud/v1alpha1/Reservation"},
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if c.ttl != defaultTTL {
		t.Fatalf("expected ttl %v, got %v", defaultTTL, c.ttl)
	}
	if c.overlay.ttl != defaultTTL {
		t.Fatalf("expected overlay ttl %v, got %v", defaultTTL, c.overlay.ttl)
	}
}

// TestNewExplicitTTL: a non-zero TTL is honoured verbatim.
func TestNewExplicitTTL(t *testing.T) {
	inner := newTestClient(t)
	c, err := New(inner, &fakeInformerSource{inf: &fakeInformer{}}, testScheme(t), Config{
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

// TestWriteErrorLeavesOverlayUntouched: for every mutating method, a failing
// inner call surfaces the error and leaves the overlay untouched (no live entry
// and no tombstone).
func TestWriteErrorLeavesOverlayUntouched(t *testing.T) {
	sentinel := errors.New("boom")
	cases := []struct {
		name string
		rv   string // ResourceVersion for the object passed to the op
		seed bool   // pre-seed the object in the inner client (delete needs it)
		// mkClient wraps an inner client (which may already contain r) with the
		// relevant injected error.
		mkClient func(inner client.Client) client.Client
		op       func(c *CachingClient, r *v1alpha1.Reservation) error
	}{
		{
			name:     "create",
			mkClient: func(inner client.Client) client.Client { return &errClient{Client: inner, createErr: sentinel} },
			op:       func(c *CachingClient, r *v1alpha1.Reservation) error { return c.Create(context.Background(), r) },
		},
		{
			name:     "update",
			rv:       "1",
			mkClient: func(inner client.Client) client.Client { return &errClient{Client: inner, updateErr: sentinel} },
			op:       func(c *CachingClient, r *v1alpha1.Reservation) error { return c.Update(context.Background(), r) },
		},
		{
			name:     "patch",
			rv:       "1",
			mkClient: func(inner client.Client) client.Client { return &errClient{Client: inner, patchErr: sentinel} },
			op: func(c *CachingClient, r *v1alpha1.Reservation) error {
				p := r.DeepCopy()
				p.Spec.AvailabilityZone = "az-new"
				return c.Patch(context.Background(), p, client.MergeFrom(r))
			},
		},
		{
			name:     "delete",
			seed:     true,
			mkClient: func(inner client.Client) client.Client { return &errClient{Client: inner, deleteErr: sentinel} },
			op:       func(c *CachingClient, r *v1alpha1.Reservation) error { return c.Delete(context.Background(), r) },
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := newReservation("res-"+tc.name, "az-1", tc.rv)
			var base client.Client
			if tc.seed {
				base = newTestClient(t, r)
			} else {
				base = newTestClient(t)
			}
			c := newCaching(t, tc.mkClient(base), &fakeInformerSource{inf: &fakeInformer{}})

			if err := tc.op(c, r); !errors.Is(err, sentinel) {
				t.Fatalf("expected sentinel error, got %v", err)
			}
			if _, ok := c.overlay.get(reservationGVK(), objectKey{name: r.Name}); ok {
				t.Fatalf("overlay must not be touched on %s failure", tc.name)
			}
		})
	}
}

// TestWriteServedFromOverlay: after a write through the caching client, a Get
// returns the written value even though the inner client has been forced to a
// divergent (stale) value behind the cache's back. This proves the read path is
// actually served from the overlay, not merely that the overlay was written.
func TestWriteServedFromOverlay(t *testing.T) {
	cases := []struct {
		name string
		// write performs the write under test through c, given the current
		// object cur fetched from inner, and returns the value it wrote.
		write func(t *testing.T, c *CachingClient, cur *v1alpha1.Reservation) string
		// diverge forces the inner client to a stale value behind the cache.
		diverge func(t *testing.T, inner client.Client, name string)
		// read extracts the field under test from a Get result.
		read func(*v1alpha1.Reservation) string
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
			c := newCaching(t, inner, &fakeInformerSource{inf: &fakeInformer{}})

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

// TestGetPropagatesNonNotFoundError: a cached-GVK Get surfaces inner errors
// other than NotFound without consulting the overlay.
func TestGetPropagatesNonNotFoundError(t *testing.T) {
	sentinel := errors.New("get boom")
	inner := &errClient{Client: newTestClient(t), getErr: sentinel}
	c := newCaching(t, inner, &fakeInformerSource{inf: &fakeInformer{}})

	// Seed a live overlay entry that must NOT mask the underlying error.
	c.overlay.upsert(reservationGVK(), newReservation("res-ge", "az-1", "1"))

	var got v1alpha1.Reservation
	err := c.Get(context.Background(), types.NamespacedName{Name: "res-ge"}, &got)
	if !errors.Is(err, sentinel) {
		t.Fatalf("expected sentinel error, got %v", err)
	}
}

// TestGetOverlayResurrectsNotFound: a live overlay entry satisfies a Get that
// the inner client reports as NotFound (write not yet visible in the informer).
func TestGetOverlayResurrectsNotFound(t *testing.T) {
	inner := newTestClient(t) // empty: inner Get returns NotFound
	c := newCaching(t, inner, &fakeInformerSource{inf: &fakeInformer{}})

	c.overlay.upsert(reservationGVK(), newReservation("res-gr", "az-z", "1"))

	var got v1alpha1.Reservation
	if err := c.Get(context.Background(), types.NamespacedName{Name: "res-gr"}, &got); err != nil {
		t.Fatalf("expected overlay to satisfy Get, got %v", err)
	}
	if got.Spec.AvailabilityZone != "az-z" {
		t.Fatalf("expected az-z from overlay, got %q", got.Spec.AvailabilityZone)
	}
}

// TestGetNotFoundWithNoOverlay: inner NotFound with no overlay entry propagates
// NotFound unchanged for a cached GVK.
func TestGetNotFoundWithNoOverlay(t *testing.T) {
	inner := newTestClient(t)
	c := newCaching(t, inner, &fakeInformerSource{inf: &fakeInformer{}})

	var got v1alpha1.Reservation
	err := c.Get(context.Background(), types.NamespacedName{Name: "missing"}, &got)
	if !apierrors.IsNotFound(err) {
		t.Fatalf("expected NotFound, got %v", err)
	}
}

// TestGetNonCachedPropagatesError: for a non-cached GVK, Get is a pure
// passthrough and surfaces the inner error verbatim.
func TestGetNonCachedPropagatesError(t *testing.T) {
	sentinel := errors.New("get boom")
	inner := &errClient{Client: newTestClient(t), getErr: sentinel}
	c, err := New(inner, &fakeInformerSource{inf: &fakeInformer{}}, testScheme(t), Config{})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	var got v1alpha1.Reservation
	if gerr := c.Get(context.Background(), types.NamespacedName{Name: "x"}, &got); !errors.Is(gerr, sentinel) {
		t.Fatalf("expected sentinel error, got %v", gerr)
	}
}

// TestListPropagatesError: for a cached GVK, a failing inner List surfaces the
// error rather than returning a partial overlay merge.
func TestListPropagatesError(t *testing.T) {
	sentinel := errors.New("list boom")
	inner := &errClient{Client: newTestClient(t), listErr: sentinel}
	c := newCaching(t, inner, &fakeInformerSource{inf: &fakeInformer{}})
	c.overlay.upsert(reservationGVK(), newReservation("res-le", "az-1", "1"))

	var list v1alpha1.ReservationList
	if err := c.List(context.Background(), &list); !errors.Is(err, sentinel) {
		t.Fatalf("expected sentinel error, got %v", err)
	}
}

// TestStatusUpdateErrorLeavesOverlayUntouched: a failed status update does not
// populate the overlay.
func TestStatusUpdateErrorLeavesOverlayUntouched(t *testing.T) {
	inner := newTestClient(t) // object absent → status update fails
	c := newCaching(t, inner, &fakeInformerSource{inf: &fakeInformer{}})

	r := newReservation("res-se", "az-1", "1")
	if err := c.Status().Update(context.Background(), r); err == nil {
		t.Fatalf("expected status update to fail for missing object")
	}
	if _, ok := c.overlay.get(reservationGVK(), objectKey{name: "res-se"}); ok {
		t.Fatalf("overlay must not be populated on status update failure")
	}
}

// TestStatusCreateDelegates: Status().Create delegates to the inner status
// writer (fake client reports it unsupported) and never touches the overlay.
func TestStatusCreateDelegates(t *testing.T) {
	r := newReservation("res-sc", "az-1", "")
	inner := newTestClient(t, r)
	c := newCaching(t, inner, &fakeInformerSource{inf: &fakeInformer{}})

	// The fake client does not support subresource Create; we only assert the
	// call is delegated (returns an error) and the overlay stays empty.
	if err := c.Status().Create(context.Background(), r, r); err == nil {
		t.Fatalf("expected Status().Create to fail on fake client")
	}
	if _, ok := c.overlay.get(reservationGVK(), objectKey{name: "res-sc"}); ok {
		t.Fatalf("Status().Create must not populate the overlay")
	}
}

// TestStatusUpdateNonCachedNoOverlay: Status().Update for a non-cached GVK does
// not touch the overlay.
func TestStatusUpdateNonCachedNoOverlay(t *testing.T) {
	r := newReservation("res-sn", "az-1", "")
	inner := newTestClient(t, r)
	c, err := New(inner, &fakeInformerSource{inf: &fakeInformer{}}, testScheme(t), Config{})
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
	if _, ok := c.overlay.get(reservationGVK(), objectKey{name: "res-sn"}); ok {
		t.Fatalf("non-cached GVK status update should not populate overlay")
	}
}

// TestGVKForUnknownType: gvkFor reports not-cached for a type not registered in
// the scheme.
func TestGVKForUnknownType(t *testing.T) {
	inner := newTestClient(t)
	c := newCaching(t, inner, &fakeInformerSource{inf: &fakeInformer{}})
	if _, cached := c.gvkFor(&unknownObject{}); cached {
		t.Fatalf("unknown type must not be reported as cached")
	}
}

// TestTrimListSuffix exercises the list-kind suffix trimming helper.
func TestTrimListSuffix(t *testing.T) {
	cases := []struct {
		in       string
		wantKind string
		wantOK   bool
	}{
		{"ReservationList", "Reservation", true},
		{"List", "List", false}, // len(kind) not > len("List")
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

// unknownObject is a client.Object whose type is not registered in the test
// scheme, used to exercise the "unresolvable GVK" branches.
type unknownObject struct {
	metav1.TypeMeta
	metav1.ObjectMeta
}

func (u *unknownObject) DeepCopyObject() runtime.Object { return u }
