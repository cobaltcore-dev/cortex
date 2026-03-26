// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package commitments

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	coordinationv1 "k8s.io/api/coordination/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func TestLocalCRMutex_Lock(t *testing.T) {
	mu := &LocalCRMutex{}
	ctx := context.Background()

	// Acquire lock
	_, unlock, err := mu.Lock(ctx)
	if err != nil {
		t.Fatalf("Lock failed: %v", err)
	}

	// Verify unlock function works
	unlock()

	// Should be able to acquire again after unlock
	_, unlock2, err := mu.Lock(ctx)
	if err != nil {
		t.Fatalf("Second lock failed: %v", err)
	}
	unlock2()
}

func TestLocalCRMutex_ConcurrentAccess(t *testing.T) {
	mu := &LocalCRMutex{}
	ctx := context.Background()
	var counter int64
	var wg sync.WaitGroup

	// Run multiple goroutines that try to increment counter
	for range 10 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, unlock, err := mu.Lock(ctx)
			if err != nil {
				t.Errorf("Lock failed: %v", err)
				return
			}
			defer unlock()
			// Critical section - should be serialized
			val := atomic.LoadInt64(&counter)
			time.Sleep(time.Millisecond)
			atomic.StoreInt64(&counter, val+1)
		}()
	}

	wg.Wait()

	// All increments should have happened
	if counter != 10 {
		t.Errorf("Expected counter=10, got %d", counter)
	}
}

func TestDistributedMutex_AcquireAndRelease(t *testing.T) {
	scheme := runtime.NewScheme()
	if err := coordinationv1.AddToScheme(scheme); err != nil {
		t.Fatalf("Failed to add scheme: %v", err)
	}

	k8sClient := fake.NewClientBuilder().
		WithScheme(scheme).
		Build()

	config := DistributedMutexConfig{
		LeaseName:      "test-mutex",
		LeaseNamespace: "default",
		LeaseDuration:  5 * time.Second,
		RetryInterval:  100 * time.Millisecond,
		AcquireTimeout: 10 * time.Second,
		HolderIdentity: "test-pod-1",
	}

	mu := NewDistributedMutex(k8sClient, config)
	ctx := context.Background()

	// Acquire lock
	_, unlock, err := mu.Lock(ctx)
	if err != nil {
		t.Fatalf("Lock failed: %v", err)
	}

	// Verify lease was created
	lease := &coordinationv1.Lease{}
	if err := k8sClient.Get(ctx, client.ObjectKey{Name: "test-mutex", Namespace: "default"}, lease); err != nil {
		t.Fatalf("Failed to get lease: %v", err)
	}

	if lease.Spec.HolderIdentity == nil || *lease.Spec.HolderIdentity != "test-pod-1" {
		t.Errorf("Expected holder identity 'test-pod-1', got %v", lease.Spec.HolderIdentity)
	}

	// Release lock
	unlock()

	// After release, the lease should have renew time in the past (expired)
	if err := k8sClient.Get(ctx, client.ObjectKey{Name: "test-mutex", Namespace: "default"}, lease); err != nil {
		t.Fatalf("Failed to get lease after release: %v", err)
	}

	// Check that the lease is now acquirable by another holder
	if lease.Spec.RenewTime != nil && lease.Spec.LeaseDurationSeconds != nil {
		expireTime := lease.Spec.RenewTime.Add(time.Duration(*lease.Spec.LeaseDurationSeconds) * time.Second)
		if time.Now().Before(expireTime) {
			t.Errorf("Lease should be expired after release, but expires at %v", expireTime)
		}
	}
}

func TestDistributedMutex_ReacquireSamePod(t *testing.T) {
	scheme := runtime.NewScheme()
	if err := coordinationv1.AddToScheme(scheme); err != nil {
		t.Fatalf("Failed to add scheme: %v", err)
	}

	k8sClient := fake.NewClientBuilder().
		WithScheme(scheme).
		Build()

	config := DistributedMutexConfig{
		LeaseName:      "test-mutex-reacquire",
		LeaseNamespace: "default",
		LeaseDuration:  5 * time.Second,
		RetryInterval:  100 * time.Millisecond,
		AcquireTimeout: 10 * time.Second,
		HolderIdentity: "test-pod-1",
	}

	mu := NewDistributedMutex(k8sClient, config)
	ctx := context.Background()

	// Acquire lock twice from the same mutex instance (same holder)
	_, unlock1, err := mu.Lock(ctx)
	if err != nil {
		t.Fatalf("First lock failed: %v", err)
	}
	unlock1()

	// Should be able to reacquire
	_, unlock2, err := mu.Lock(ctx)
	if err != nil {
		t.Fatalf("Second lock failed: %v", err)
	}
	unlock2()
}

func TestDistributedMutex_Timeout(t *testing.T) {
	scheme := runtime.NewScheme()
	if err := coordinationv1.AddToScheme(scheme); err != nil {
		t.Fatalf("Failed to add scheme: %v", err)
	}

	k8sClient := fake.NewClientBuilder().
		WithScheme(scheme).
		Build()

	// First mutex acquires the lock
	config1 := DistributedMutexConfig{
		LeaseName:      "test-mutex-timeout",
		LeaseNamespace: "default",
		LeaseDuration:  30 * time.Second, // Long lease
		RetryInterval:  50 * time.Millisecond,
		AcquireTimeout: 10 * time.Second,
		HolderIdentity: "holder-1",
	}
	mu1 := NewDistributedMutex(k8sClient, config1)
	ctx := context.Background()

	_, _, err := mu1.Lock(ctx)
	if err != nil {
		t.Fatalf("First lock failed: %v", err)
	}
	// Don't release - keep the lock held

	// Second mutex tries to acquire with short timeout
	config2 := DistributedMutexConfig{
		LeaseName:      "test-mutex-timeout",
		LeaseNamespace: "default",
		LeaseDuration:  5 * time.Second,
		RetryInterval:  50 * time.Millisecond,
		AcquireTimeout: 200 * time.Millisecond, // Short timeout
		HolderIdentity: "holder-2",
	}
	mu2 := NewDistributedMutex(k8sClient, config2)

	_, _, err = mu2.Lock(ctx)
	if err == nil {
		t.Error("Expected timeout error, but lock succeeded")
	}
}
