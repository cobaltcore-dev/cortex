// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package commitments

import (
	"context"
	"fmt"
	"os"
	"sync"
	"time"

	"github.com/go-logr/logr"
	coordinationv1 "k8s.io/api/coordination/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"
)

const (
	// DefaultLeaseDuration is how long a lease is valid before it can be taken over
	DefaultLeaseDuration = 30 * time.Second
	// DefaultRetryInterval is how often to retry acquiring the lease
	DefaultRetryInterval = 500 * time.Millisecond
	// DefaultAcquireTimeout is the maximum time to wait for acquiring the lease
	DefaultAcquireTimeout = 60 * time.Second
)

// DistributedMutexConfig holds configuration for the distributed mutex
type DistributedMutexConfig struct {
	// LeaseName is the name of the Lease resource
	LeaseName string
	// LeaseNamespace is the namespace where the Lease resource is created
	LeaseNamespace string
	// LeaseDuration is how long the lease is valid
	LeaseDuration time.Duration
	// RetryInterval is how often to retry acquiring the lease
	RetryInterval time.Duration
	// AcquireTimeout is the maximum time to wait for the lease
	AcquireTimeout time.Duration
	// HolderIdentity identifies this instance (typically pod name)
	HolderIdentity string
}

// DefaultDistributedMutexConfig returns a config with sensible defaults
func DefaultDistributedMutexConfig() DistributedMutexConfig {
	hostname, err := os.Hostname()
	if err != nil {
		hostname = "unknown-host"
	}
	return DistributedMutexConfig{
		LeaseName:      "cortex-cr-mutex",
		LeaseNamespace: "default",
		LeaseDuration:  DefaultLeaseDuration,
		RetryInterval:  DefaultRetryInterval,
		AcquireTimeout: DefaultAcquireTimeout,
		HolderIdentity: hostname,
	}
}

// DistributedMutex provides distributed locking using Kubernetes Leases.
// It serializes CR state changes between the syncer and change-commitments API
// across different pods/deployments.
type DistributedMutex struct {
	client client.Client
	config DistributedMutexConfig
	logger logr.Logger

	// localMu prevents concurrent Lock() calls from the same pod
	localMu sync.Mutex
	// held tracks if this instance currently holds the lease
	held bool
}

// NewDistributedMutex creates a new distributed mutex using Kubernetes Leases
func NewDistributedMutex(k8sClient client.Client, config DistributedMutexConfig) *DistributedMutex {
	return &DistributedMutex{
		client: k8sClient,
		config: config,
		logger: log.Log.WithName("distributed-mutex"),
	}
}

// Lock acquires the distributed lock, blocking until successful or context is cancelled.
// Returns a context that should be used for the locked operation and an unlock function.
func (m *DistributedMutex) Lock(ctx context.Context) (context.Context, func(), error) {
	m.localMu.Lock()

	// Create a timeout context for acquisition
	acquireCtx, acquireCancel := context.WithTimeout(ctx, m.config.AcquireTimeout)
	defer acquireCancel()

	ticker := time.NewTicker(m.config.RetryInterval)
	defer ticker.Stop()

	startTime := time.Now()
	attempts := 0

	for {
		attempts++
		acquired, err := m.tryAcquire(acquireCtx)
		if err != nil {
			m.localMu.Unlock()
			return ctx, nil, fmt.Errorf("failed to acquire lease: %w", err)
		}
		if acquired {
			m.held = true
			m.logger.V(1).Info("acquired distributed lock",
				"leaseName", m.config.LeaseName,
				"holder", m.config.HolderIdentity,
				"attempts", attempts,
				"duration", time.Since(startTime))

			// Return unlock function that releases both local and distributed lock
			unlockOnce := sync.Once{}
			unlock := func() {
				unlockOnce.Do(func() {
					m.release(context.Background()) // Use background context for cleanup
					m.held = false
					m.localMu.Unlock()
				})
			}
			return ctx, unlock, nil
		}

		select {
		case <-acquireCtx.Done():
			m.localMu.Unlock()
			return ctx, nil, fmt.Errorf("timeout waiting for distributed lock after %d attempts: %w", attempts, acquireCtx.Err())
		case <-ticker.C:
			// Continue trying
		}
	}
}

// tryAcquire attempts to acquire or renew the lease once
func (m *DistributedMutex) tryAcquire(ctx context.Context) (bool, error) {
	now := metav1.NewMicroTime(time.Now())
	leaseDurationSeconds := int32(m.config.LeaseDuration.Seconds())

	lease := &coordinationv1.Lease{}
	err := m.client.Get(ctx, types.NamespacedName{
		Name:      m.config.LeaseName,
		Namespace: m.config.LeaseNamespace,
	}, lease)

	if errors.IsNotFound(err) {
		// Create new lease
		newLease := &coordinationv1.Lease{
			ObjectMeta: metav1.ObjectMeta{
				Name:      m.config.LeaseName,
				Namespace: m.config.LeaseNamespace,
			},
			Spec: coordinationv1.LeaseSpec{
				HolderIdentity:       &m.config.HolderIdentity,
				LeaseDurationSeconds: &leaseDurationSeconds,
				AcquireTime:          &now,
				RenewTime:            &now,
			},
		}
		if err := m.client.Create(ctx, newLease); err != nil {
			if errors.IsAlreadyExists(err) {
				// Race condition - another pod created it first, retry
				return false, nil
			}
			return false, err
		}
		return true, nil
	}
	if err != nil {
		return false, err
	}

	// Check if we already hold the lease
	if lease.Spec.HolderIdentity != nil && *lease.Spec.HolderIdentity == m.config.HolderIdentity {
		// Renew our own lease
		lease.Spec.RenewTime = &now
		if err := m.client.Update(ctx, lease); err != nil {
			if errors.IsConflict(err) {
				return false, nil // Retry
			}
			return false, err
		}
		return true, nil
	}

	// Check if lease has expired
	if lease.Spec.RenewTime != nil && lease.Spec.LeaseDurationSeconds != nil {
		expireTime := lease.Spec.RenewTime.Add(time.Duration(*lease.Spec.LeaseDurationSeconds) * time.Second)
		if time.Now().After(expireTime) {
			// Lease expired, try to take over
			lease.Spec.HolderIdentity = &m.config.HolderIdentity
			lease.Spec.AcquireTime = &now
			lease.Spec.RenewTime = &now
			lease.Spec.LeaseDurationSeconds = &leaseDurationSeconds
			if err := m.client.Update(ctx, lease); err != nil {
				if errors.IsConflict(err) {
					return false, nil // Another pod took it first, retry
				}
				return false, err
			}
			m.logger.Info("took over expired lease",
				"leaseName", m.config.LeaseName,
				"previousHolder", lease.Spec.HolderIdentity)
			return true, nil
		}
	}

	// Lease is held by another pod and not expired
	m.logger.V(1).Info("waiting for lease",
		"leaseName", m.config.LeaseName,
		"currentHolder", *lease.Spec.HolderIdentity)
	return false, nil
}

// release releases the distributed lock
func (m *DistributedMutex) release(ctx context.Context) {
	lease := &coordinationv1.Lease{}
	err := m.client.Get(ctx, types.NamespacedName{
		Name:      m.config.LeaseName,
		Namespace: m.config.LeaseNamespace,
	}, lease)
	if err != nil {
		m.logger.Error(err, "failed to get lease for release")
		return
	}

	// Only release if we hold it
	if lease.Spec.HolderIdentity == nil || *lease.Spec.HolderIdentity != m.config.HolderIdentity {
		m.logger.V(1).Info("not releasing lease - not held by us",
			"leaseName", m.config.LeaseName,
			"holder", lease.Spec.HolderIdentity)
		return
	}

	// Set expire time to now by setting renew time in the past
	pastTime := metav1.NewMicroTime(time.Now().Add(-m.config.LeaseDuration))
	lease.Spec.RenewTime = &pastTime

	if err := m.client.Update(ctx, lease); err != nil {
		m.logger.Error(err, "failed to release lease")
		return
	}
	m.logger.V(1).Info("released distributed lock", "leaseName", m.config.LeaseName)
}

// CRMutexInterface defines the interface for CR state serialization.
// This allows switching between local (in-memory) and distributed (Lease-based) implementations.
type CRMutexInterface interface {
	// Lock acquires the lock. Returns an unlock function that must be called to release.
	// The returned context should be used for the locked operation.
	Lock(ctx context.Context) (context.Context, func(), error)
}

// Ensure implementations satisfy the interface
var _ CRMutexInterface = (*DistributedMutex)(nil)
var _ CRMutexInterface = (*LocalCRMutex)(nil)

// LocalCRMutex is an in-memory mutex for single-pod deployments or testing.
// It wraps sync.Mutex to implement CRMutexInterface.
type LocalCRMutex struct {
	mu sync.Mutex
}

// Lock acquires the local mutex
func (m *LocalCRMutex) Lock(ctx context.Context) (context.Context, func(), error) {
	m.mu.Lock()
	unlockOnce := sync.Once{}
	unlock := func() {
		unlockOnce.Do(func() {
			m.mu.Unlock()
		})
	}
	return ctx, unlock, nil
}
