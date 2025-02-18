// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package openstack

import (
	"context"
	"log/slog"
	gosync "sync"

	"github.com/cobaltcore-dev/cortex/internal/conf"
	"github.com/cobaltcore-dev/cortex/internal/db"
	"github.com/cobaltcore-dev/cortex/internal/sync"
	"github.com/prometheus/client_golang/prometheus"
)

type Syncer interface {
	Init(context.Context)
	Sync(context.Context) error
}

// Combined syncer that combines multiple syncers.
type CombinedSyncer struct {
	monitor sync.Monitor
	// List of syncers to run in parallel.
	syncers []Syncer
}

// Create a new combined syncer that runs multiple syncers in parallel.
func NewCombinedSyncer(
	ctx context.Context,
	config conf.SyncOpenStackConfig,
	monitor sync.Monitor,
	db db.DB,
) sync.Datasource {

	keystoneAPI := newKeystoneAPI(config.Keystone)
	slog.Info("loading openstack sub-syncers")
	syncers := []Syncer{
		newNovaSyncer(db, monitor, keystoneAPI, config.Nova),
		newPlacementSyncer(db, monitor, keystoneAPI, config.Placement),
	}
	return CombinedSyncer{monitor: monitor, syncers: syncers}
}

// Create all needed database tables if they do not exist.
func (s CombinedSyncer) Init(ctx context.Context) {
	for _, syncer := range s.syncers {
		syncer.Init(ctx)
	}
}

// Sync all objects from OpenStack to the database.
func (s CombinedSyncer) Sync(context context.Context) {
	if s.monitor.PipelineRunTimer != nil {
		hist := s.monitor.PipelineRunTimer.WithLabelValues("openstack")
		timer := prometheus.NewTimer(hist)
		defer timer.ObserveDuration()
	}

	// Sync all objects in parallel.
	var wg gosync.WaitGroup
	for _, syncer := range s.syncers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := syncer.Sync(context); err != nil {
				slog.Error("failed to sync objects", "error", err)
			}
		}()
	}
	wg.Wait()
}
