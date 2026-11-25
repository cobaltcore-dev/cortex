// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

// Package external provides access to external data sources for scheduling.
package external

import (
	"context"
	"fmt"
	"sync"

	"github.com/cobaltcore-dev/cortex/api/v1alpha1"
	"github.com/cobaltcore-dev/cortex/pkg/knowledge/store"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// StoreReader provides read access to the store of a datasource.
// It reads the storage configuration from a Datasource CRD.
type StoreReader struct {
	// Kubernetes client to read the storage secrets.
	Client client.Client
	// Storage in which the datasource objects are persisted.
	Storage v1alpha1.StorageSpec
	// Mutex to protect lazy initialization of the store.
	mu sync.Mutex
	// Cached store (lazily initialized).
	store store.Store
}

// NewStoreReader creates a new StoreReader from a Datasource CRD name.
// It looks up the Datasource CRD to get the storage configuration.
func NewStoreReader(ctx context.Context, c client.Client, datasourceName string) (*StoreReader, error) {
	datasource := &v1alpha1.Datasource{}
	if err := c.Get(ctx, client.ObjectKey{Name: datasourceName}, datasource); err != nil {
		return nil, fmt.Errorf("failed to get datasource %s: %w", datasourceName, err)
	}
	return &StoreReader{Client: c, Storage: datasource.Spec.Storage}, nil
}

// Store returns the store, connecting to it if necessary.
// This method is safe for concurrent use.
func (r *StoreReader) Store(ctx context.Context) (store.Store, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.store != nil {
		return r.store, nil
	}
	st, err := store.Open(ctx, r.Client, r.Storage)
	if err != nil {
		return nil, fmt.Errorf("failed to connect to storage: %w", err)
	}
	r.store = st
	return r.store, nil
}

// List all objects of the given model from the store.
func listAll[T store.Model](ctx context.Context, r *StoreReader) ([]T, error) {
	st, err := r.Store(ctx)
	if err != nil {
		return nil, err
	}
	return store.ListAll[T](ctx, st, "")
}
