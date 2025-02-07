// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package openstack

import (
	"log/slog"
	gosync "sync"

	"github.com/cobaltcore-dev/cortex/internal/conf"
	"github.com/cobaltcore-dev/cortex/internal/db"
	"github.com/cobaltcore-dev/cortex/internal/sync"
	"github.com/prometheus/client_golang/prometheus"
)

var supportedSyncers = map[string]func(
	db.DB,
	conf.SyncOpenStackConfig,
	sync.Monitor,
) Syncer{
	"nova_server":     newNovaSyncer[Server, ServerList],
	"nova_hypervisor": newNovaSyncer[Hypervisor, HypervisorList],
	"placement":       newPlacementSyncer,
}

type CombinedSyncer struct {
	Syncers  []Syncer
	Keystone KeystoneAPI
	monitor  sync.Monitor
}

func NewCombinedSyncer(config conf.SyncOpenStackConfig, db db.DB, monitor sync.Monitor) sync.Datasource {
	slog.Info("loading openstack syncers", "types", config.Types)
	syncers := []Syncer{}
	for _, typeName := range config.Types {
		syncer, ok := supportedSyncers[typeName]
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
