// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package datasources

import (
	"fmt"
	"sync"

	"github.com/cobaltcore-dev/cortex/api/v1alpha1"
	"github.com/cobaltcore-dev/cortex/pkg/knowledge/store"
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
