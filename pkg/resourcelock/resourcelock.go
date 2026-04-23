// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

// Package resourcelock provides distributed locking for Kubernetes resources
// using coordination.k8s.io/v1 Lease objects.
//
// Locks are short-lived (held for milliseconds during a single write) and are
// not renewed in the background. This makes the implementation simpler than a
// full leader-election library while still being safe for serializing
// concurrent access to a shared resource (e.g. a ConfigMap) across replicas.
//
// Usage:
//
//	rl := resourcelock.NewResourceLocker(client, "my-namespace")
//	if err := rl.AcquireLock(ctx, "my-lock", "holder-abc"); err != nil { ... }
//	defer rl.ReleaseLock(ctx, "my-lock", "holder-abc")
package resourcelock

import (
	"errors"
	"fmt"
	"time"

	coordinationv1 "k8s.io/api/coordination/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	logf "sigs.k8s.io/controller-runtime/pkg/log"

	"context"
)

// ErrLockHeld is returned when a lock cannot be acquired within the retry
// timeout because another locker holds it.
var ErrLockHeld = errors.New("resourcelock: lock is held by another locker")

// ResourceLocker acquires and releases distributed locks backed by Kubernetes
// Lease resources. Each lock is a single Lease object in the configured
// namespace. The locker creates the Lease on acquire and deletes it on release,
// so no Lease resources linger after normal operation.
type ResourceLocker struct {
	client        client.Client
	namespace     string
	leaseDuration int32         // seconds; how long a lease is valid before it expires
	retryInterval time.Duration // pause between acquire attempts when the lease is held
	retryTimeout  time.Duration // total time to keep retrying before returning ErrLockHeld
}

// Option configures a ResourceLocker.
type Option func(*ResourceLocker)

// WithLeaseDuration sets the lease duration. Default is 15 seconds.
func WithLeaseDuration(d time.Duration) Option {
	return func(rl *ResourceLocker) { rl.leaseDuration = int32(d.Seconds()) }
}

// WithRetryInterval sets the interval between acquire retries. Default is 500ms.
func WithRetryInterval(d time.Duration) Option {
	return func(rl *ResourceLocker) { rl.retryInterval = d }
}

// WithRetryTimeout sets the total time to retry acquiring a held lock. Default is 5s.
func WithRetryTimeout(d time.Duration) Option {
	return func(rl *ResourceLocker) { rl.retryTimeout = d }
}

// NewResourceLocker creates a ResourceLocker that manages Lease resources in
// the given namespace. Configure behaviour with functional options; the
// defaults are tuned for short-lived locks that protect a single API call.
func NewResourceLocker(c client.Client, namespace string, opts ...Option) *ResourceLocker {
	rl := &ResourceLocker{
		client:        c,
		namespace:     namespace,
		leaseDuration: 15,
		retryInterval: 500 * time.Millisecond,
		retryTimeout:  5 * time.Second,
	}
	for _, o := range opts {
		o(rl)
	}
	return rl
}

// AcquireLock tries to acquire a Lease-backed lock identified by name.
//
// The algorithm has three cases:
//  1. Lease does not exist — create it and return (lock acquired).
//  2. Lease exists but is expired — update it to claim ownership.
//  3. Lease exists and is still valid — sleep and retry until the timeout,
//     then return ErrLockHeld.
//
// Create and Update conflicts (another replica raced us) are retried
// transparently within the timeout window.
func (rl *ResourceLocker) AcquireLock(ctx context.Context, name, lockerID string) error {
	log := logf.FromContext(ctx).WithValues("namespace", rl.namespace, "lease", name, "locker", lockerID)

	deadline := time.Now().Add(rl.retryTimeout)
	for {
		now := metav1.NewMicroTime(time.Now())
		lease := &coordinationv1.Lease{}
		err := rl.client.Get(ctx, client.ObjectKey{Namespace: rl.namespace, Name: name}, lease)

		// Case 1: Lease does not exist — create it to acquire the lock.
		if apierrors.IsNotFound(err) {
			lease = &coordinationv1.Lease{
				ObjectMeta: metav1.ObjectMeta{
					Name:      name,
					Namespace: rl.namespace,
				},
				Spec: coordinationv1.LeaseSpec{
					HolderIdentity:       &lockerID,
					LeaseDurationSeconds: &rl.leaseDuration,
					AcquireTime:          &now,
					RenewTime:            &now,
				},
			}
			if err := rl.client.Create(ctx, lease); err != nil {
				// Another replica created it between our Get and Create.
				if apierrors.IsAlreadyExists(err) {
					if time.Now().After(deadline) {
						return ErrLockHeld
					}
					time.Sleep(rl.retryInterval)
					continue
				}
				return fmt.Errorf("resourcelock: create lease %s: %w", name, err)
			}
			log.V(2).Info("Lock acquired, lease created")
			return nil
		}
		if err != nil {
			return fmt.Errorf("resourcelock: get lease %s: %w", name, err)
		}

		// Case 3: Lease is still valid — wait and retry.
		if lease.Spec.RenewTime != nil && lease.Spec.LeaseDurationSeconds != nil {
			expiry := lease.Spec.RenewTime.Add(time.Duration(*lease.Spec.LeaseDurationSeconds) * time.Second)
			if time.Now().Before(expiry) {
				if time.Now().After(deadline) {
					return ErrLockHeld
				}
				time.Sleep(rl.retryInterval)
				continue
			}
		}

		// Case 2: Lease expired — claim it by updating the holder.
		previousHolder := ""
		if lease.Spec.HolderIdentity != nil {
			previousHolder = *lease.Spec.HolderIdentity
		}
		lease.Spec.HolderIdentity = &lockerID
		lease.Spec.LeaseDurationSeconds = &rl.leaseDuration
		lease.Spec.AcquireTime = &now
		lease.Spec.RenewTime = &now
		if err := rl.client.Update(ctx, lease); err != nil {
			// Another replica claimed the expired lease first.
			if apierrors.IsConflict(err) {
				if time.Now().After(deadline) {
					return ErrLockHeld
				}
				time.Sleep(rl.retryInterval)
				continue
			}
			return fmt.Errorf("resourcelock: update lease %s: %w", name, err)
		}
		log.V(2).Info("Lock acquired, claimed expired lease", "previousHolder", previousHolder)
		return nil
	}
}

// ReleaseLock deletes the Lease if it is held by the given lockerID. If the
// Lease does not exist or is held by a different locker, this is a no-op.
// The holder check prevents a slow replica from accidentally releasing a lock
// that was already re-acquired by someone else.
func (rl *ResourceLocker) ReleaseLock(ctx context.Context, name, lockerID string) error {
	lease := &coordinationv1.Lease{}
	if err := rl.client.Get(ctx, client.ObjectKey{Namespace: rl.namespace, Name: name}, lease); err != nil {
		if apierrors.IsNotFound(err) {
			return nil
		}
		return fmt.Errorf("resourcelock: get lease %s: %w", name, err)
	}
	// Only the current holder may release — avoids deleting a lease that was
	// re-acquired by another replica after our lock expired.
	if lease.Spec.HolderIdentity != nil && *lease.Spec.HolderIdentity != lockerID {
		return nil
	}
	if err := rl.client.Delete(ctx, lease); err != nil && !apierrors.IsNotFound(err) {
		return fmt.Errorf("resourcelock: delete lease %s: %w", name, err)
	}
	return nil
}
