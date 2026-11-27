// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

// Package store abstracts where datasource objects are persisted, so that
// syncers, extractors and kpis don't depend on a specific database.
package store

import (
	"context"
	"fmt"
	"sync"

	"github.com/cobaltcore-dev/cortex/api/v1alpha1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// Model is a go type of which objects are persisted in a store.
type Model interface {
	// Name of the table/collection the objects are stored in.
	TableName() string
	// Map of index name to column names.
	Indexes() map[string][]string
}

// PartitionedModel is a model of which multiple datasources share one table,
// e.g. prometheus metrics, which are partitioned by their name/alias.
type PartitionedModel interface {
	Model
	// Name of the column holding the partition key.
	PartitionColumn() string
}

// Scope selects the objects of one datasource in a store.
type Scope struct {
	// Model of the stored objects.
	Model Model
	// Partition of the objects, required for partitioned models only.
	Partition string
}

// Store persists datasource objects.
type Store interface {
	// ListAll objects in the scope into out, which must be a pointer to a
	// slice of the scope's model type. The order of the objects is undefined.
	ListAll(ctx context.Context, scope Scope, out any) error
	// ReplaceAll objects in the scope atomically with objs, which must be a
	// slice of the scope's model type.
	ReplaceAll(ctx context.Context, scope Scope, objs any) error
}

// ListAll objects of the given model and partition.
func ListAll[T Model](ctx context.Context, s Store, partition string) ([]T, error) {
	var model T
	var out []T
	if err := s.ListAll(ctx, Scope{Model: model, Partition: partition}, &out); err != nil {
		return nil, err
	}
	return out, nil
}

// ReplaceAll objects of the given model and partition.
func ReplaceAll[T Model](ctx context.Context, s Store, partition string, objs []T) error {
	var model T
	return s.ReplaceAll(ctx, Scope{Model: model, Partition: partition}, objs)
}

// Opener connects to a store backend described by a storage spec.
type Opener func(ctx context.Context, c client.Client, spec v1alpha1.StorageSpec) (Store, error)

var (
	backendsMu sync.RWMutex
	backends   = map[v1alpha1.StorageType]Opener{}
)

// Register a store backend for the given storage type. Backends register
// themselves through a func init() in their own package.
func Register(t v1alpha1.StorageType, opener Opener) {
	backendsMu.Lock()
	defer backendsMu.Unlock()
	backends[t] = opener
}

// Open the store described by the given storage spec.
func Open(ctx context.Context, c client.Client, spec v1alpha1.StorageSpec) (Store, error) {
	backendsMu.RLock()
	opener, ok := backends[spec.Type]
	backendsMu.RUnlock()
	if !ok {
		return nil, fmt.Errorf("unsupported storage type %q", spec.Type)
	}
	return opener(ctx, c, spec)
}
