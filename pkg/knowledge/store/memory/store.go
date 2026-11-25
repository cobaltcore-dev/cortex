// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

// Package memory provides an in-process store, e.g. as backend for tests.
package memory

import (
	"context"
	"sync"

	"github.com/cobaltcore-dev/cortex/pkg/knowledge/store"
)

// Store keeps encoded objects in memory, with the same semantics as a
// persistent store.
type Store struct {
	mu sync.RWMutex
	// Encoded objects by scope key.
	scopes map[string][][]byte
}

// New creates an empty in-memory store.
func New() *Store {
	return &Store{scopes: map[string][][]byte{}}
}

// ListAll objects in the scope into out, a pointer to a slice of the model.
func (s *Store) ListAll(ctx context.Context, scope store.Scope, out any) error {
	if err := scope.Validate(); err != nil {
		return err
	}
	s.mu.RLock()
	data, ok := s.scopes[scope.Key()]
	s.mu.RUnlock()
	if !ok {
		return store.ErrNotFound
	}
	list, err := store.Decode(scope, data)
	if err != nil {
		return err
	}
	return store.SetList(scope, out, list)
}

// ReplaceAll objects in the scope with objs, a slice of the model.
func (s *Store) ReplaceAll(ctx context.Context, scope store.Scope, objs any) error {
	entries, err := store.Encode(scope, objs)
	if err != nil {
		return err
	}
	data := make([][]byte, len(entries))
	for i, e := range entries {
		data[i] = e.Data
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.scopes[scope.Key()] = data
	return nil
}

// Exists reports whether the scope was written.
func (s *Store) Exists(ctx context.Context, scope store.Scope) (bool, error) {
	if err := scope.Validate(); err != nil {
		return false, err
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	_, ok := s.scopes[scope.Key()]
	return ok, nil
}

// Clear all scopes, e.g. to simulate a restart of a non-persistent store.
func (s *Store) Clear() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.scopes = map[string][][]byte{}
}
