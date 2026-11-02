// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package openstack

import (
	"github.com/cobaltcore-dev/cortex/api/v1alpha1"
	"github.com/cobaltcore-dev/cortex/internal/knowledge/datasources"
	"github.com/cobaltcore-dev/cortex/internal/knowledge/db"
	"github.com/cobaltcore-dev/cortex/pkg/keystone"
)

// Index of all supported OpenStack syncers, keyed by datasource type.
// Each syncer registers itself here through a func init() in its own package.
var Index = map[v1alpha1.OpenStackDatasourceType]func(
	datasource v1alpha1.Datasource,
	authenticatedDB *db.DB,
	authenticatedKeystone keystone.KeystoneClient,
	monitor datasources.Monitor,
) Syncer{}
