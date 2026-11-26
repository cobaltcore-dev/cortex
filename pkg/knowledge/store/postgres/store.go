// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package postgres

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"sync"

	"github.com/cobaltcore-dev/cortex/api/v1alpha1"
	"github.com/cobaltcore-dev/cortex/pkg/knowledge/store"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

func init() {
	store.Register(v1alpha1.StorageTypePostgres, func(ctx context.Context, c client.Client, spec v1alpha1.StorageSpec) (store.Store, error) {
		if spec.Postgres == nil {
			return nil, errors.New("postgres storage spec is missing")
		}
		db, err := Connector{Client: c}.FromSecretRef(ctx, spec.Postgres.SecretRef)
		if err != nil {
			return nil, err
		}
		return StoreFor(db), nil
	})
}

// Store implements store.Store on top of a database connection.
// Tables are created lazily when they are first accessed.
type Store struct {
	db *DB
	// Guards the table mappings of the underlying gorp DbMap, which are
	// not safe for concurrent modification.
	mu      sync.RWMutex
	ensured map[string]struct{}
}

// Stores by database connection, so that table mappings are shared.
var stores sync.Map // map[*DB]*Store

// NewStore creates a new store on top of the given database connection.
func NewStore(db *DB) *Store {
	return &Store{db: db, ensured: map[string]struct{}{}}
}

// StoreFor returns the shared store of the given database connection.
func StoreFor(db *DB) *Store {
	s, _ := stores.LoadOrStore(db, NewStore(db))
	return s.(*Store)
}

// Create the table of the model if it doesn't exist yet.
func (s *Store) ensure(model store.Model) error {
	s.mu.RLock()
	_, ok := s.ensured[model.TableName()]
	s.mu.RUnlock()
	if ok {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.ensured[model.TableName()]; ok {
		return nil
	}
	if err := s.db.CreateTable(s.db.AddTable(model)); err != nil {
		return err
	}
	s.ensured[model.TableName()] = struct{}{}
	return nil
}

// Build the where clause selecting the scope's partition, if any.
func partitionFilter(scope store.Scope) (where string, args map[string]any, err error) {
	partitioned, ok := scope.Model.(store.PartitionedModel)
	if !ok {
		if scope.Partition != "" {
			return "", nil, fmt.Errorf("model %T is not partitioned", scope.Model)
		}
		return "", nil, nil
	}
	if scope.Partition == "" {
		return "", nil, fmt.Errorf("model %T is partitioned, but no partition is given", scope.Model)
	}
	return partitioned.PartitionColumn() + " = :partition", map[string]any{"partition": scope.Partition}, nil
}

// ListAll objects in the scope into out, a pointer to a slice of the model.
func (s *Store) ListAll(ctx context.Context, scope store.Scope, out any) error {
	modelType := reflect.TypeOf(scope.Model)
	outType := reflect.TypeOf(out)
	if outType == nil || outType.Kind() != reflect.Pointer || outType.Elem().Kind() != reflect.Slice ||
		outType.Elem().Elem() != modelType {
		return fmt.Errorf("expected *[]%s, got %T", modelType, out)
	}
	if err := s.ensure(scope.Model); err != nil {
		return err
	}
	where, args, err := partitionFilter(scope)
	if err != nil {
		return err
	}
	query := "SELECT * FROM " + scope.Model.TableName()
	var params []any
	if where != "" {
		query += " WHERE " + where
		params = append(params, args)
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if _, err := s.db.WithContext(ctx).Select(out, query, params...); err != nil {
		return fmt.Errorf("failed to list objects from %s: %w", scope.Model.TableName(), err)
	}
	return nil
}

// ReplaceAll objects in the scope with objs, a slice of the model.
func (s *Store) ReplaceAll(ctx context.Context, scope store.Scope, objs any) error {
	modelType := reflect.TypeOf(scope.Model)
	val := reflect.ValueOf(objs)
	if val.Kind() != reflect.Slice || val.Type().Elem() != modelType {
		return fmt.Errorf("expected []%s, got %T", modelType, objs)
	}
	where, args, err := partitionFilter(scope)
	if err != nil {
		return err
	}
	if where != "" {
		if err := checkPartition(scope, val); err != nil {
			return err
		}
	}
	if err := s.ensure(scope.Model); err != nil {
		return err
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	return replaceAll(ctx, *s.db, scope.Model.TableName(), where, args, val)
}

// Ensure all objects belong to the scope's partition, so that replacing one
// partition never writes objects into another one.
func checkPartition(scope store.Scope, objs reflect.Value) error {
	column := scope.Model.(store.PartitionedModel).PartitionColumn()
	modelType := objs.Type().Elem()
	fieldIdx := -1
	for i := range modelType.NumField() {
		if name, _, _ := strings.Cut(modelType.Field(i).Tag.Get("db"), ","); name == column {
			fieldIdx = i
			break
		}
	}
	if fieldIdx < 0 {
		return fmt.Errorf("model %s has no field for partition column %q", modelType, column)
	}
	for i := range objs.Len() {
		if got := objs.Index(i).Field(fieldIdx).String(); got != scope.Partition {
			return fmt.Errorf("object %d belongs to partition %q, expected %q", i, got, scope.Partition)
		}
	}
	return nil
}
