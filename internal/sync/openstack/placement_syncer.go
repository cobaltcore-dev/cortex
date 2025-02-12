// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package openstack

import (
	"log/slog"
	gosync "sync"
	"time"

	"github.com/cobaltcore-dev/cortex/internal/conf"
	"github.com/cobaltcore-dev/cortex/internal/db"
	"github.com/cobaltcore-dev/cortex/internal/sync"
)

// Syncer for placement objects.
type placementSyncer struct {
	// Configuration for the syncer.
	Config conf.SyncOpenStackConfig
	// Placement API to fetch objects from.
	API PlacementAPI
	// Database to insert the objects into.
	DB db.DB
	// Monitor to observe the syncer.
	monitor sync.Monitor
	// Sleep interval to avoid overloading the API.
	sleepInterval time.Duration
}

// Create a new placement syncer with some default values.
func newPlacementSyncer(
	db db.DB,
	config conf.SyncOpenStackConfig,
	monitor sync.Monitor,
) Syncer {

	return &placementSyncer{
		Config:        config,
		API:           NewPlacementAPI(config, monitor),
		DB:            db,
		monitor:       monitor,
		sleepInterval: 50 * time.Millisecond,
	}
}

// Create the necessary database tables if they do not exist.
func (s *placementSyncer) Init() {
	if err := s.DB.CreateTable(
		s.DB.AddTable(ResourceProvider{}),
		s.DB.AddTable(ResourceProviderTrait{}),
		s.DB.AddTable(ResourceProviderAggregate{}),
	); err != nil {
		panic(err)
	}
}

// Sync resource provider objects from OpenStack to the database.
func (s *placementSyncer) syncProviders(auth KeystoneAuth) ([]ResourceProvider, error) {
	tx, err := s.DB.Begin()
	if err != nil {
		slog.Error("failed to begin transaction", "error", err)
		return nil, tx.Rollback()
	}
	tableName := (&ResourceProvider{}).TableName()
	if _, err = tx.Exec("DELETE FROM " + tableName); err != nil {
		slog.Error("failed to delete old objects", "error", err)
		return nil, tx.Rollback()
	}
	providers, err := s.API.ListResourceProviders(auth)
	if err != nil {
		slog.Error("failed to get resource providers", "error", err)
		return nil, tx.Rollback()
	}
	for _, provider := range providers {
		if err = tx.Insert(&provider); err != nil {
			slog.Error("failed to insert obj", "model", "openstack_resource_providers", "error", err)
			return nil, tx.Rollback()
		}
	}
	if err = tx.Commit(); err != nil {
		slog.Error("failed to commit transaction", "error", err)
		return nil, err
	}
	if s.monitor.PipelineObjectsGauge != nil {
		g := s.monitor.PipelineObjectsGauge
		g.WithLabelValues("openstack_resource_providers").Set(float64(len(providers)))
	}
	slog.Info("synced objects", "model", "openstack_resource_providers", "n", len(providers))
	return providers, nil
}

// Sync resource provider details (e.g. traits and aggregates) for each provider
// from OpenStack to the database.
func syncProviderDetails[M ProviderDetail](
	s *placementSyncer,
	auth KeystoneAuth,
	providers []ResourceProvider,
	fetchFunc func(KeystoneAuth, ResourceProvider) ([]M, error),
) error {

	var model M

	resultMutex := gosync.Mutex{}
	results := []M{}
	var wg gosync.WaitGroup
	for _, provider := range providers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			newResults, err := fetchFunc(auth, provider)
			if err != nil {
				slog.Error("failed to get placement data", "error", err)
				return
			}
			resultMutex.Lock()
			results = append(results, newResults...)
			resultMutex.Unlock()
		}()
		time.Sleep(s.sleepInterval) // Don't overload the API.
	}
	wg.Wait()

	tx, err := s.DB.Begin()
	if err != nil {
		slog.Error("failed to begin transaction", "error", err)
		return tx.Rollback()
	}
	if _, err = tx.Exec("DELETE FROM " + model.TableName()); err != nil {
		slog.Error("failed to delete old objects", "error", err)
		return tx.Rollback()
	}
	modelName := model.GetName()
	for _, result := range results {
		if err = tx.Insert(&result); err != nil {
			slog.Error("failed to insert obj", "modelName", modelName, "error", err)
			return tx.Rollback()
		}
	}
	if err = tx.Commit(); err != nil {
		slog.Error("failed to commit transaction", "error", err)
		return err
	}
	if s.monitor.PipelineObjectsGauge != nil {
		g := s.monitor.PipelineObjectsGauge
		g.WithLabelValues(modelName).Set(float64(len(results)))
	}
	slog.Info("synced objects", "model", modelName, "n", len(results))
	return nil
}

// Sync all needed placement objects from OpenStack to the database.
func (s *placementSyncer) Sync(auth KeystoneAuth) error {
	providers, err := s.syncProviders(auth)
	if err != nil {
		return err
	}
	// Sync traits.
	err = syncProviderDetails(s, auth, providers, s.API.ResolveTraits)
	if err != nil {
		return err
	}
	// Sync aggregates.
	err = syncProviderDetails(s, auth, providers, s.API.ResolveAggregates)
	if err != nil {
		return err
	}
	return nil
}
