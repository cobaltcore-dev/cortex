// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package openstack

import (
	"github.com/cobaltcore-dev/cortex/api/v1alpha1"
	"github.com/cobaltcore-dev/cortex/pkg/keystone"
	"github.com/cobaltcore-dev/cortex/pkg/knowledge/datasources"
	"github.com/cobaltcore-dev/cortex/pkg/knowledge/store"
)

// Index of all supported OpenStack syncers, keyed by datasource type.
// Each syncer registers itself here through a func init() in its own package.
var Index = map[v1alpha1.OpenStackDatasourceType]func(
	datasource v1alpha1.Datasource,
	st store.Store,
	authenticatedKeystone keystone.KeystoneClient,
	monitor datasources.Monitor,
) Syncer{}

// RegisterModels registers the models stored by an openstack datasource type,
// keyed by the subtype returned by the given function, e.g. "servers" for nova.
func RegisterModels(
	t v1alpha1.OpenStackDatasourceType,
	subtype func(v1alpha1.OpenStackDatasource) string,
	models map[string]store.Model,
) {

	datasources.RegisterScope(func(spec v1alpha1.DatasourceSpec) (store.Scope, bool) {
		if spec.Type != v1alpha1.DatasourceTypeOpenStack || spec.OpenStack.Type != t {
			return store.Scope{}, false
		}
		model, ok := models[subtype(spec.OpenStack)]
		if !ok {
			return store.Scope{}, false
		}
		return store.Scope{Model: model}, true
	})
}
