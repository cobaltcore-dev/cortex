// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package openstack

import (
	"log/slog"
	"time"

	"github.com/cobaltcore-dev/cortex/internal/conf"
	"github.com/cobaltcore-dev/cortex/internal/db"
	"github.com/cobaltcore-dev/cortex/internal/sync"
	"github.com/go-pg/pg/v10/orm"
)

type placementSyncer struct {
	Config  conf.SyncOpenStackConfig
	API     PlacementAPI
	DB      db.DB
	monitor sync.Monitor
}

func newPlacementSyncer(
	db db.DB,
	config conf.SyncOpenStackConfig,
	monitor sync.Monitor,
) Syncer {
	return &placementSyncer{
		Config:  config,
		API:     NewPlacementAPI(config, monitor),
		DB:      db,
		monitor: monitor,
	}
}

// Create the necessary database tables if they do not exist.
func (s *placementSyncer) Init() {
	models := []any{
		(*ResourceProvider)(nil),
		(*ResourceProviderTrait)(nil),
		(*ResourceProviderAggregate)(nil),
	}
	for _, model := range models {
		if err := s.DB.Get().Model(model).CreateTable(&orm.CreateTableOptions{
			IfNotExists: true,
		}); err != nil {
			panic(err)
		}
	}
}

func (s *placementSyncer) Sync(auth KeystoneAuth) error {
	tx, err := s.DB.Get().Begin()
	if err != nil {
		slog.Error("failed to begin transaction", "error", err)
		return tx.Rollback()
	}

	models := []any{
		(*ResourceProvider)(nil),
		(*ResourceProviderTrait)(nil),
		(*ResourceProviderAggregate)(nil),
	}
	for _, model := range models {
		if _, err = tx.Model(model).Where("TRUE").Delete(); err != nil {
			slog.Error("failed to delete old objects", "error", err)
			return tx.Rollback()
		}
	}

	providers, err := s.API.ListResourceProviders(auth)
	if err != nil {
		slog.Error("failed to get resource providers", "error", err)
		return tx.Rollback()
	}
	if _, err = tx.Model(&providers).
		OnConflict("(uuid) DO UPDATE").
		Insert(); err != nil {
		slog.Error("failed to insert objects", "model", "openstack_resource_providers", "error", err)
		return tx.Rollback()
	}

	traits := []ResourceProviderTrait{}
	for _, provider := range providers {
		providerTraits, err := s.API.ResolveTraits(auth, provider)
		if err != nil {
			slog.Error("failed to get resource provider traits", "error", err)
			return tx.Rollback()
		}
		traits = append(traits, providerTraits...)
		time.Sleep(50 * time.Millisecond) // Don't overload the API.
	}
	if _, err = tx.Model(&traits).
		OnConflict("(name) DO UPDATE").
		Insert(); err != nil {
		slog.Error("failed to insert objects", "model", "openstack_resource_provider_traits", "error", err)
		return tx.Rollback()
	}

	aggregates := []ResourceProviderAggregate{}
	for _, provider := range providers {
		providerAggregates, err := s.API.ResolveAggregates(auth, provider)
		if err != nil {
			slog.Error("failed to get resource provider aggregates", "error", err)
			return tx.Rollback()
		}
		aggregates = append(aggregates, providerAggregates...)
		time.Sleep(50 * time.Millisecond) // Don't overload the API.
	}
	if _, err = tx.Model(&traits).
		OnConflict("(uuid) DO UPDATE").
		Insert(); err != nil {
		slog.Error("failed to insert objects", "model", "openstack_resource_provider_aggregates", "error", err)
		return tx.Rollback()
	}

	if err = tx.Commit(); err != nil {
		slog.Error("failed to commit transaction", "error", err)
		return err
	}
	if s.monitor.PipelineObjectsGauge != nil {
		g := s.monitor.PipelineObjectsGauge
		g.WithLabelValues("openstack_resource_providers").Set(float64(len(providers)))
		g.WithLabelValues("openstack_resource_provider_traits").Set(float64(len(traits)))
		g.WithLabelValues("openstack_resource_provider_aggregates").Set(float64(len(aggregates)))
	}
	slog.Info("synced objects", "type", "openstack_resource_providers", "n", len(providers))
	slog.Info("synced objects", "type", "openstack_resource_provider_traits", "n", len(traits))
	slog.Info("synced objects", "type", "openstack_resource_provider_aggregates", "n", len(aggregates))
	return nil
}
