// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package datasources

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/cobaltcore-dev/cortex/api/v1alpha1"
	"github.com/cobaltcore-dev/cortex/pkg/knowledge/store"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// ScopeResolver returns the store scope in which the objects of the given
// datasource are stored, or false if the datasource is not handled by it.
type ScopeResolver func(spec v1alpha1.DatasourceSpec) (store.Scope, bool)

var (
	scopeResolversMu sync.RWMutex
	scopeResolvers   []ScopeResolver
)

// RegisterScope registers a scope resolver. Datasource implementations
// register themselves through a func init() in their own package.
func RegisterScope(r ScopeResolver) {
	scopeResolversMu.Lock()
	defer scopeResolversMu.Unlock()
	scopeResolvers = append(scopeResolvers, r)
}

// ScopeFor returns the store scope of the given datasource, which also
// determines the go model type of the datasource objects.
func ScopeFor(spec v1alpha1.DatasourceSpec) (store.Scope, error) {
	scopeResolversMu.RLock()
	defer scopeResolversMu.RUnlock()
	for _, resolve := range scopeResolvers {
		if scope, ok := resolve(spec); ok {
			return scope, nil
		}
	}
	return store.Scope{}, fmt.Errorf("no registered model for datasource of type %q", spec.Type)
}

// StoreCheckInterval is how often datasources that are not yet due for their
// next sync check that their objects are still stored. Stores may lose their
// data, e.g. when a non-persistent valkey restarts, in which case the
// datasource is synced again right away.
const StoreCheckInterval = time.Minute

// IsStored reports whether the objects of the given datasource are stored.
func IsStored(ctx context.Context, c client.Client, ds v1alpha1.Datasource) (bool, error) {
	scope, err := ScopeFor(ds.Spec)
	if err != nil {
		return false, err
	}
	st, err := store.Open(ctx, c, ds.Spec.Storage)
	if err != nil {
		return false, err
	}
	return st.Exists(ctx, scope)
}
