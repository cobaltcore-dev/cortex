// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package placement

import (
	"context"

	"github.com/cobaltcore-dev/cortex/knowledge/api/datasources/openstack/placement"
	"github.com/cobaltcore-dev/cortex/knowledge/api/v1alpha1"
	"github.com/cobaltcore-dev/cortex/knowledge/internal/datasources"
	"github.com/cobaltcore-dev/cortex/lib/db"
	"github.com/go-gorp/gorp"
)

// Syncer for OpenStack placement.
type PlacementSyncer struct {
	// Database to store the placement objects in.
	DB db.DB
	// Monitor to track the syncer.
	Mon datasources.Monitor
	// Configuration for the placement syncer.
	Conf v1alpha1.PlacementDatasource
	// Placement API client to fetch the data.
	API PlacementAPI
}

// Init the OpenStack resource provider and trait syncer.
func (s *PlacementSyncer) Init(ctx context.Context) error {
	s.API.Init(ctx)
	var tables = []*gorp.TableMap{}
	// Only add the tables that are configured in the yaml conf.
	switch s.Conf.Type {
	case v1alpha1.PlacementDatasourceTypeResourceProviders:
		tables = append(tables, s.DB.AddTable(placement.ResourceProvider{}))
	case v1alpha1.PlacementDatasourceTypeResourceProviderTraits:
		tables = append(tables, s.DB.AddTable(placement.Trait{}))
	case v1alpha1.PlacementDatasourceTypeResourceProviderInventoryUsages:
		tables = append(tables, s.DB.AddTable(placement.InventoryUsage{}))
	}
	return s.DB.CreateTable(tables...)
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
	if err := db.ReplaceAll(s.DB, rps...); err != nil {
		return 0, err
	}
	label := placement.ResourceProvider{}.TableName()
	if s.Mon.PipelineObjectsGauge != nil {
		gauge := s.Mon.PipelineObjectsGauge.WithLabelValues(label)
		gauge.Set(float64(len(rps)))
	}
	if s.Mon.PipelineRequestProcessedCounter != nil {
		counter := s.Mon.PipelineRequestProcessedCounter.WithLabelValues(label)
		counter.Inc()
	}
	return int64(len(rps)), nil
}

// Sync the OpenStack traits into the database.
func (s *PlacementSyncer) SyncTraits(ctx context.Context) (int64, error) {
	var rps []placement.ResourceProvider
	_, err := s.DB.Select(&rps, "SELECT * FROM "+placement.ResourceProvider{}.TableName())
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
	if err := db.ReplaceAll(s.DB, traits...); err != nil {
		return 0, err
	}
	label := placement.Trait{}.TableName()
	if s.Mon.PipelineObjectsGauge != nil {
		gauge := s.Mon.PipelineObjectsGauge.WithLabelValues(label)
		gauge.Set(float64(len(traits)))
	}
	if s.Mon.PipelineRequestProcessedCounter != nil {
		counter := s.Mon.PipelineRequestProcessedCounter.WithLabelValues(label)
		counter.Inc()
	}
	return int64(len(traits)), err
}

// Sync the OpenStack resource provider inventories and usages into the database.
func (s *PlacementSyncer) SyncInventoryUsages(ctx context.Context) (int64, error) {
	var rps []placement.ResourceProvider
	_, err := s.DB.Select(&rps, "SELECT * FROM "+placement.ResourceProvider{}.TableName())
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
	if err := db.ReplaceAll(s.DB, inventoryUsages...); err != nil {
		return 0, err
	}
	label := placement.InventoryUsage{}.TableName()
	if s.Mon.PipelineObjectsGauge != nil {
		gauge := s.Mon.PipelineObjectsGauge.WithLabelValues(label)
		gauge.Set(float64(len(inventoryUsages)))
	}
	if s.Mon.PipelineRequestProcessedCounter != nil {
		counter := s.Mon.PipelineRequestProcessedCounter.WithLabelValues(label)
		counter.Inc()
	}
	return int64(len(inventoryUsages)), err
}
