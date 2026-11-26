// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package placement

import (
	"context"

	"github.com/cobaltcore-dev/cortex/api/v1alpha1"
	"github.com/cobaltcore-dev/cortex/pkg/keystone"
	"github.com/cobaltcore-dev/cortex/pkg/knowledge/datasources"
	"github.com/cobaltcore-dev/cortex/pkg/knowledge/datasources/openstack"
	"github.com/cobaltcore-dev/cortex/pkg/knowledge/store"
)

// Syncer for OpenStack
type PlacementSyncer struct {
	// Store to persist the placement objects in.
	Store store.Store
	// Monitor to track the syncer.
	Mon datasources.Monitor
	// Configuration for the placement syncer.
	Conf v1alpha1.PlacementDatasource
	// Placement API client to fetch the data.
	API PlacementAPI
}

// Init the OpenStack resource provider and trait syncer.
func (s *PlacementSyncer) Init(ctx context.Context) error {
	return s.API.Init(ctx)
}

// Sync the OpenStack placement objects.
func (s *PlacementSyncer) Sync(ctx context.Context) (int64, error) {
	// Only sync the objects that are configured in the yaml conf.
	var err error
	var nResults int64
	switch s.Conf.Type {
	case v1alpha1.PlacementDatasourceTypeResourceProviders:
		nResults, err = s.SyncResourceProviders(ctx)
	case v1alpha1.PlacementDatasourceTypeResourceProviderTraits:
		nResults, err = s.SyncTraits(ctx)
	case v1alpha1.PlacementDatasourceTypeResourceProviderInventoryUsages:
		nResults, err = s.SyncInventoryUsages(ctx)
	}
	return nResults, err
}

// Sync the OpenStack resource providers into the database.
func (s *PlacementSyncer) SyncResourceProviders(ctx context.Context) (int64, error) {
	rps, err := s.API.GetAllResourceProviders(ctx)
	if err != nil {
		return 0, err
	}
	if err := store.ReplaceAll(ctx, s.Store, "", rps); err != nil {
		return 0, err
	}
	label := ResourceProvider{}.TableName()
	if s.Mon.ObjectsGauge != nil {
		gauge := s.Mon.ObjectsGauge.WithLabelValues(label)
		gauge.Set(float64(len(rps)))
	}
	if s.Mon.RequestProcessedCounter != nil {
		counter := s.Mon.RequestProcessedCounter.WithLabelValues(label)
		counter.Inc()
	}
	return int64(len(rps)), nil
}

// Sync the OpenStack traits into the database.
func (s *PlacementSyncer) SyncTraits(ctx context.Context) (int64, error) {
	rps, err := store.ListAll[ResourceProvider](ctx, s.Store, "")
	if err != nil {
		return 0, v1alpha1.ErrWaitingForDependencyDatasource
	}
	if len(rps) == 0 {
		return 0, v1alpha1.ErrWaitingForDependencyDatasource
	}
	traits, err := s.API.GetAllTraits(ctx, rps)
	if err != nil {
		return 0, err
	}
	if err := store.ReplaceAll(ctx, s.Store, "", traits); err != nil {
		return 0, err
	}
	label := Trait{}.TableName()
	if s.Mon.ObjectsGauge != nil {
		gauge := s.Mon.ObjectsGauge.WithLabelValues(label)
		gauge.Set(float64(len(traits)))
	}
	if s.Mon.RequestProcessedCounter != nil {
		counter := s.Mon.RequestProcessedCounter.WithLabelValues(label)
		counter.Inc()
	}
	return int64(len(traits)), err
}

// Sync the OpenStack resource provider inventories and usages into the database.
func (s *PlacementSyncer) SyncInventoryUsages(ctx context.Context) (int64, error) {
	rps, err := store.ListAll[ResourceProvider](ctx, s.Store, "")
	if err != nil {
		return 0, v1alpha1.ErrWaitingForDependencyDatasource
	}
	if len(rps) == 0 {
		return 0, v1alpha1.ErrWaitingForDependencyDatasource
	}
	inventoryUsages, err := s.API.GetAllInventoryUsages(ctx, rps)
	if err != nil {
		return 0, err
	}
	if err := store.ReplaceAll(ctx, s.Store, "", inventoryUsages); err != nil {
		return 0, err
	}
	label := InventoryUsage{}.TableName()
	if s.Mon.ObjectsGauge != nil {
		gauge := s.Mon.ObjectsGauge.WithLabelValues(label)
		gauge.Set(float64(len(inventoryUsages)))
	}
	if s.Mon.RequestProcessedCounter != nil {
		counter := s.Mon.RequestProcessedCounter.WithLabelValues(label)
		counter.Inc()
	}
	return int64(len(inventoryUsages)), err
}

func init() {
	openstack.RegisterModels(
		v1alpha1.OpenStackDatasourceTypePlacement,
		func(ds v1alpha1.OpenStackDatasource) string { return string(ds.Placement.Type) },
		map[string]store.Model{
			string(v1alpha1.PlacementDatasourceTypeResourceProviders):               ResourceProvider{},
			string(v1alpha1.PlacementDatasourceTypeResourceProviderTraits):          Trait{},
			string(v1alpha1.PlacementDatasourceTypeResourceProviderInventoryUsages): InventoryUsage{},
		},
	)
	openstack.Index[v1alpha1.OpenStackDatasourceTypePlacement] = func(
		datasource v1alpha1.Datasource,
		st store.Store,
		authenticatedKeystone keystone.KeystoneClient,
		monitor datasources.Monitor,
	) openstack.Syncer {

		return &PlacementSyncer{
			Store: st,
			Mon:   monitor,
			Conf:  datasource.Spec.OpenStack.Placement,
			API:   NewPlacementAPI(monitor, authenticatedKeystone, datasource.Spec.OpenStack.Placement),
		}
	}
}
