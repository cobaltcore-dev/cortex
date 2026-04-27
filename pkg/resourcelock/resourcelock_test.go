// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package resourcelock

import (
	"context"
	"errors"
	"testing"
	"time"

	coordinationv1 "k8s.io/api/coordination/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func newFakeClient(t *testing.T) client.Client {
	t.Helper()
	s := runtime.NewScheme()
	if err := coordinationv1.AddToScheme(s); err != nil {
		t.Fatalf("add coordination scheme: %v", err)
	}
	return fake.NewClientBuilder().WithScheme(s).Build()
}

func TestAcquireAndRelease(t *testing.T) {
	cl := newFakeClient(t)
	rl := NewResourceLocker(cl, "default")
	ctx := context.Background()

	if err := rl.AcquireLock(ctx, "my-lock", "holder-1"); err != nil {
		t.Fatalf("AcquireLock: %v", err)
	}

	lease := &coordinationv1.Lease{}
	if err := cl.Get(ctx, client.ObjectKey{Namespace: "default", Name: "my-lock"}, lease); err != nil {
		t.Fatalf("get lease: %v", err)
	}
	if lease.Spec.HolderIdentity == nil || *lease.Spec.HolderIdentity != "holder-1" {
		t.Fatalf("holder = %v, want holder-1", lease.Spec.HolderIdentity)
	}

	if err := rl.ReleaseLock(ctx, "my-lock", "holder-1"); err != nil {
		t.Fatalf("ReleaseLock: %v", err)
	}

	err := cl.Get(ctx, client.ObjectKey{Namespace: "default", Name: "my-lock"}, lease)
	if err == nil {
		t.Fatal("expected lease to be deleted")
	}
}

func TestAcquireAlreadyHeld(t *testing.T) {
	cl := newFakeClient(t)
	rl := NewResourceLocker(cl, "default",
		WithRetryTimeout(100*time.Millisecond),
		WithRetryInterval(20*time.Millisecond),
	)
	ctx := context.Background()

	if err := rl.AcquireLock(ctx, "my-lock", "holder-1"); err != nil {
		t.Fatalf("AcquireLock: %v", err)
	}

	err := rl.AcquireLock(ctx, "my-lock", "holder-2")
	if !errors.Is(err, ErrLockHeld) {
		t.Fatalf("got %v, want ErrLockHeld", err)
	}
}

func TestReleaseNotOwner(t *testing.T) {
	cl := newFakeClient(t)
	rl := NewResourceLocker(cl, "default")
	ctx := context.Background()

	if err := rl.AcquireLock(ctx, "my-lock", "holder-1"); err != nil {
		t.Fatalf("AcquireLock: %v", err)
	}

	if err := rl.ReleaseLock(ctx, "my-lock", "holder-2"); err != nil {
		t.Fatalf("ReleaseLock: %v", err)
	}

	lease := &coordinationv1.Lease{}
	if err := cl.Get(ctx, client.ObjectKey{Namespace: "default", Name: "my-lock"}, lease); err != nil {
		t.Fatal("expected lease to still exist after release by non-owner")
	}
}

func TestReleaseNotFound(t *testing.T) {
	cl := newFakeClient(t)
	rl := NewResourceLocker(cl, "default")

	if err := rl.ReleaseLock(context.Background(), "nonexistent", "holder-1"); err != nil {
		t.Fatalf("ReleaseLock: %v", err)
	}
}

func TestWithOptions(t *testing.T) {
	cl := newFakeClient(t)
	rl := NewResourceLocker(cl, "test-ns",
		WithLeaseDuration(30*time.Second),
		WithRetryInterval(100*time.Millisecond),
		WithRetryTimeout(1*time.Second),
	)

	if rl.leaseDuration != 30 {
		t.Errorf("leaseDuration = %d, want 30", rl.leaseDuration)
	}
	if rl.retryInterval != 100*time.Millisecond {
		t.Errorf("retryInterval = %v, want 100ms", rl.retryInterval)
	}
	if rl.retryTimeout != 1*time.Second {
		t.Errorf("retryTimeout = %v, want 1s", rl.retryTimeout)
	}
	if rl.namespace != "test-ns" {
		t.Errorf("namespace = %q, want test-ns", rl.namespace)
	}
}
