// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package openstack

import (
	"errors"

	"github.com/cobaltcore-dev/cortex/api/v1alpha1"
	"github.com/cobaltcore-dev/cortex/internal/knowledge/datasources"
	"github.com/cobaltcore-dev/cortex/internal/knowledge/datasources/plugins/openstack/cinder"
	"github.com/cobaltcore-dev/cortex/internal/knowledge/datasources/plugins/openstack/identity"
	"github.com/cobaltcore-dev/cortex/internal/knowledge/datasources/plugins/openstack/limes"
	"github.com/cobaltcore-dev/cortex/internal/knowledge/datasources/plugins/openstack/manila"
	"github.com/cobaltcore-dev/cortex/internal/knowledge/datasources/plugins/openstack/nova"
	"github.com/cobaltcore-dev/cortex/internal/knowledge/datasources/plugins/openstack/placement"
	"github.com/cobaltcore-dev/cortex/internal/knowledge/db"
	"github.com/cobaltcore-dev/cortex/pkg/keystone"
)

// Get the supported syncer for the given datasource.
func getSupportedSyncer(
	datasource v1alpha1.Datasource,
	authenticatedDB *db.DB,
	authenticatedKeystone keystone.KeystoneClient,
	monitor datasources.Monitor,
) (Syncer, error) {

	switch datasource.Spec.OpenStack.Type {
	case v1alpha1.OpenStackDatasourceTypeNova:
		return &nova.NovaSyncer{
			DB:   *authenticatedDB,
			Mon:  monitor,
			Conf: datasource.Spec.OpenStack.Nova,
			API:  nova.NewNovaAPI(monitor, authenticatedKeystone, datasource.Spec.OpenStack.Nova),
		}, nil
	case v1alpha1.OpenStackDatasourceTypeManila:
		return &manila.ManilaSyncer{
			DB:   *authenticatedDB,
			Mon:  monitor,
			Conf: datasource.Spec.OpenStack.Manila,
			API:  manila.NewManilaAPI(monitor, authenticatedKeystone, datasource.Spec.OpenStack.Manila),
		}, nil
	case v1alpha1.OpenStackDatasourceTypePlacement:
		return &placement.PlacementSyncer{
			DB:   *authenticatedDB,
			Mon:  monitor,
			Conf: datasource.Spec.OpenStack.Placement,
			API:  placement.NewPlacementAPI(monitor, authenticatedKeystone, datasource.Spec.OpenStack.Placement),
		}, nil
	case v1alpha1.OpenStackDatasourceTypeIdentity:
		return &identity.IdentitySyncer{
			DB:   *authenticatedDB,
			Mon:  monitor,
			Conf: datasource.Spec.OpenStack.Identity,
			API:  identity.NewIdentityAPI(monitor, authenticatedKeystone, datasource.Spec.OpenStack.Identity),
		}, nil
	case v1alpha1.OpenStackDatasourceTypeLimes:
		return &limes.LimesSyncer{
			DB:   *authenticatedDB,
			Mon:  monitor,
			Conf: datasource.Spec.OpenStack.Limes,
			API:  limes.NewLimesAPI(monitor, authenticatedKeystone, datasource.Spec.OpenStack.Limes),
		}, nil
	case v1alpha1.OpenStackDatasourceTypeCinder:
		return &cinder.CinderSyncer{
			DB:   *authenticatedDB,
			Mon:  monitor,
			Conf: datasource.Spec.OpenStack.Cinder,
			API:  cinder.NewCinderAPI(monitor, authenticatedKeystone, datasource.Spec.OpenStack.Cinder),
		}, nil
	default:
		return nil, errors.New("unsupported openstack datasource type")
	}
}
