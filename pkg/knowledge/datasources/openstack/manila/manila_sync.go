// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package manila

import (
	"context"

	"github.com/cobaltcore-dev/cortex/api/v1alpha1"
	"github.com/cobaltcore-dev/cortex/pkg/keystone"
	"github.com/cobaltcore-dev/cortex/pkg/knowledge/datasources"
	"github.com/cobaltcore-dev/cortex/pkg/knowledge/datasources/openstack"
	"github.com/cobaltcore-dev/cortex/pkg/knowledge/store"
)

// Syncer for OpenStack
type ManilaSyncer struct {
	// Store to persist the manila objects in.
	Store store.Store
	// Monitor to track the syncer.
	Mon datasources.Monitor
	// Configuration for the manila syncer.
	Conf v1alpha1.ManilaDatasource
	// Manila API client to fetch the data.
	API ManilaAPI
}

// Init the OpenStack manila syncer.
func (s *ManilaSyncer) Init(ctx context.Context) error {
	return s.API.Init(ctx)
}

// Sync the OpenStack manila objects and publish triggers.
func (s *ManilaSyncer) Sync(ctx context.Context) (int64, error) {
	// Only sync the objects that are configured in the yaml conf.
	var err error
	var nResults int64
	if s.Conf.Type == v1alpha1.ManilaDatasourceTypeStoragePools {
		nResults, err = s.SyncAllStoragePools(ctx)
	}
	return nResults, err
}

// Sync the OpenStack resource providers into the database.
func (s *ManilaSyncer) SyncAllStoragePools(ctx context.Context) (int64, error) {
	pools, err := s.API.GetAllStoragePools(ctx)
	if err != nil {
		return 0, err
	}
	if err := store.ReplaceAll(ctx, s.Store, "", pools); err != nil {
		return 0, err
	}
	label := StoragePool{}.TableName()
	if s.Mon.ObjectsGauge != nil {
		gauge := s.Mon.ObjectsGauge.WithLabelValues(label)
		gauge.Set(float64(len(pools)))
	}
	if s.Mon.RequestProcessedCounter != nil {
		counter := s.Mon.RequestProcessedCounter.WithLabelValues(label)
		counter.Inc()
	}
	return int64(len(pools)), nil
}

func init() {
	openstack.RegisterModels(
		v1alpha1.OpenStackDatasourceTypeManila,
		func(ds v1alpha1.OpenStackDatasource) string { return string(ds.Manila.Type) },
		map[string]store.Model{
			string(v1alpha1.ManilaDatasourceTypeStoragePools): StoragePool{},
		},
	)
	openstack.Index[v1alpha1.OpenStackDatasourceTypeManila] = func(
		datasource v1alpha1.Datasource,
		st store.Store,
		authenticatedKeystone keystone.KeystoneClient,
		monitor datasources.Monitor,
	) openstack.Syncer {

		return &ManilaSyncer{
			Store: st,
			Mon:   monitor,
			Conf:  datasource.Spec.OpenStack.Manila,
			API:   NewManilaAPI(monitor, authenticatedKeystone, datasource.Spec.OpenStack.Manila),
		}
	}
}
