// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package openstack

import (
	"fmt"
	"log/slog"
	gosync "sync"

	"github.com/cobaltcore-dev/cortex/internal/conf"
	"github.com/cobaltcore-dev/cortex/internal/db"
	"github.com/cobaltcore-dev/cortex/internal/sync"
	"github.com/prometheus/client_golang/prometheus"

	"github.com/go-pg/pg/v10/orm"
)

type CombinedSyncer struct {
	Syncers  []Syncer
	Keystone KeystoneAPI
	monitor  sync.Monitor
}

func NewCombinedSyncer(config conf.SyncOpenStackConfig, db db.DB, monitor sync.Monitor) sync.Datasource {
	slog.Info("loading openstack syncers", "types", config.Types)
	syncers := []Syncer{}
	for _, typeName := range config.Types {
		syncer, ok := supportedTypes[typeName]
		if !ok {
			panic("unknown openstack syncer type: " + typeName)
		}
		syncers = append(syncers, syncer(db, config, monitor))
	}
	return CombinedSyncer{
		Syncers:  syncers,
		Keystone: NewKeystoneAPI(config, monitor),
		monitor:  monitor,
	}
}

func (s CombinedSyncer) Init() {
	for _, syncer := range s.Syncers {
		syncer.Init()
	}
}

func (s CombinedSyncer) Sync() {
	if s.monitor.PipelineRunTimer != nil {
		hist := s.monitor.PipelineRunTimer.WithLabelValues("openstack")
		timer := prometheus.NewTimer(hist)
		defer timer.ObserveDuration()
	}

	// Authenticate with Keystone.
	auth, err := s.Keystone.Authenticate()
	if err != nil {
		slog.Error("failed to authenticate with Keystone", "error", err)
		return
	}

	// Sync all objects in parallel.
	var wg gosync.WaitGroup
	for _, syncer := range s.Syncers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := syncer.Sync(*auth); err != nil {
				slog.Error("failed to sync objects", "error", err)
			}
		}()
	}
	wg.Wait()
}

type Syncer interface {
	Init()
	Sync(auth KeystoneAuth) error
}

type syncer[M OpenStackModel, L OpenStackList] struct {
	Config  conf.SyncOpenStackConfig
	API     ObjectAPI[M, L]
	DB      db.DB
	monitor sync.Monitor
}

func newSyncerOfType[M OpenStackModel, L OpenStackList](
	db db.DB,
	config conf.SyncOpenStackConfig,
	monitor sync.Monitor,
) Syncer {

	return &syncer[M, L]{
		Config:  config,
		API:     NewObjectAPI[M, L](config, monitor),
		DB:      db,
		monitor: monitor,
	}
}

// Create the necessary database tables if they do not exist.
func (s *syncer[M, L]) Init() {
	if err := s.DB.Get().Model((*M)(nil)).CreateTable(&orm.CreateTableOptions{
		IfNotExists: true,
	}); err != nil {
		panic(err)
	}
}

func (s *syncer[M, L]) Sync(auth KeystoneAuth) error {
	var model M

	tx, err := s.DB.Get().Begin()
	if err != nil {
		slog.Error("failed to begin transaction", "error", err)
		return tx.Rollback()
	}

	if _, err = tx.Model(&model).Where("TRUE").Delete(); err != nil {
		slog.Error("failed to delete old servers", "error", err)
		return tx.Rollback()
	}
	modelName := model.GetName()
	var list []M
	list, err = s.API.List(auth)
	if err != nil {
		slog.Error("failed to get object list", "model", modelName, "error", err)
		return tx.Rollback()
	}
	const batchSize = 100
	for i := 0; i < len(list); i += batchSize {
		objs := list[i:min(i+batchSize, len(list))]
		if _, err = tx.Model(&objs).
			OnConflict(fmt.Sprintf("(%s) DO UPDATE", model.GetPKField())).
			Insert(); err != nil {
			slog.Error("failed to insert objects", "model", modelName, "error", err)
			return tx.Rollback()
		}
	}
	if err = tx.Commit(); err != nil {
		slog.Error("failed to commit transaction", "error", err)
		return err
	}
	if s.monitor.PipelineObjectsGauge != nil {
		s.monitor.PipelineObjectsGauge.
			WithLabelValues(modelName).
			Set(float64(len(list)))
	}
	slog.Info("synced objects", "model", modelName, "n", len(list))
	return nil
}
