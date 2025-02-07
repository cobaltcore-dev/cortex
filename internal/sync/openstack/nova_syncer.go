// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package openstack

import (
	"fmt"
	"log/slog"

	"github.com/cobaltcore-dev/cortex/internal/conf"
	"github.com/cobaltcore-dev/cortex/internal/db"
	"github.com/cobaltcore-dev/cortex/internal/sync"
	"github.com/go-pg/pg/v10/orm"
)

type novaSyncer[M NovaModel, L NovaList] struct {
	Config  conf.SyncOpenStackConfig
	API     NovaAPI[M, L]
	DB      db.DB
	monitor sync.Monitor
}

func newNovaSyncer[M NovaModel, L NovaList](
	db db.DB,
	config conf.SyncOpenStackConfig,
	monitor sync.Monitor,
) Syncer {
	return &novaSyncer[M, L]{
		Config:  config,
		API:     NewNovaAPI[M, L](config, monitor),
		DB:      db,
		monitor: monitor,
	}
}

// Create the necessary database tables if they do not exist.
func (s *novaSyncer[M, L]) Init() {
	if err := s.DB.Get().Model((*M)(nil)).CreateTable(&orm.CreateTableOptions{
		IfNotExists: true,
	}); err != nil {
		panic(err)
	}
}

func (s *novaSyncer[M, L]) Sync(auth KeystoneAuth) error {
	var model M

	tx, err := s.DB.Get().Begin()
	if err != nil {
		slog.Error("failed to begin transaction", "error", err)
		return tx.Rollback()
	}

	modelName := model.GetName()
	if _, err = tx.Model(&model).Where("TRUE").Delete(); err != nil {
		slog.Error("failed to delete old objects", "model", modelName, "error", err)
		return tx.Rollback()
	}
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
