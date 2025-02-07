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

func (s *placementSyncer) syncProviders(auth KeystoneAuth) ([]ResourceProvider, error) {
	tx, err := s.DB.Get().Begin()
	if err != nil {
		slog.Error("failed to begin transaction", "error", err)
		return nil, tx.Rollback()
	}
	if _, err = tx.Model((*ResourceProvider)(nil)).Where("TRUE").Delete(); err != nil {
		slog.Error("failed to delete old objects", "error", err)
		return nil, tx.Rollback()
	}
	providers, err := s.API.ListResourceProviders(auth)
	if err != nil {
		slog.Error("failed to get resource providers", "error", err)
		return nil, tx.Rollback()
	}
	if _, err = tx.Model(&providers).
		OnConflict("(uuid) DO UPDATE").
		Insert(); err != nil {
		slog.Error("failed to insert objects", "model", "openstack_resource_providers", "error", err)
		return nil, tx.Rollback()
	}
	if err = tx.Commit(); err != nil {
		slog.Error("failed to commit transaction", "error", err)
		return nil, err
	}
	if s.monitor.PipelineObjectsGauge != nil {
		g := s.monitor.PipelineObjectsGauge
		g.WithLabelValues("openstack_resource_providers").Set(float64(len(providers)))
	}
	return providers, nil
}

func (s *placementSyncer) syncTraits(auth KeystoneAuth, providers []ResourceProvider) error {
	traits := gosync.Map{}
	var wg gosync.WaitGroup
	for _, provider := range providers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			providerTraits, err := s.API.ResolveTraits(auth, provider)
			if err != nil {
				slog.Error("failed to get resource provider traits", "error", err)
				return
			}
			for _, trait := range providerTraits {
				traits.Store(trait.Name+trait.ResourceProviderUUID, trait)
			}
		}()
		time.Sleep(50 * time.Millisecond) // Don't overload the API.
	}
	wg.Wait()
	traitsSlice := []ResourceProviderTrait{}
	traits.Range(func(key, value any) bool {
		traitsSlice = append(traitsSlice, value.(ResourceProviderTrait))
		return true
	})

	tx, err := s.DB.Get().Begin()
	if err != nil {
		slog.Error("failed to begin transaction", "error", err)
		return tx.Rollback()
	}
	if _, err = tx.Model((*ResourceProviderTrait)(nil)).Where("TRUE").Delete(); err != nil {
		slog.Error("failed to delete old objects", "error", err)
		return tx.Rollback()
	}
	if _, err = tx.Model(&traitsSlice).
		OnConflict("(name) DO UPDATE").
		Insert(); err != nil {
		slog.Error("failed to insert objects", "model", "openstack_resource_provider_traits", "error", err)
		return tx.Rollback()
	}
	if err = tx.Commit(); err != nil {
		slog.Error("failed to commit transaction", "error", err)
		return err
	}
	if s.monitor.PipelineObjectsGauge != nil {
		g := s.monitor.PipelineObjectsGauge
		g.WithLabelValues("openstack_resource_provider_traits").Set(float64(len(traitsSlice)))
	}
	slog.Info("synced objects", "model", "openstack_resource_provider_traits", "n", len(traitsSlice))
	return nil
}

func (s *placementSyncer) Sync(auth KeystoneAuth) error {
	providers, err := s.syncProviders(auth)
	if err != nil {
		return err
	}
	if err := s.syncTraits(auth, providers); err != nil {
		return err
	}

	return nil
}
