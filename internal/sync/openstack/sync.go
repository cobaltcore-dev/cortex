// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package openstack

import (
	"context"
	"log/slog"
	gosync "sync"

	"github.com/cobaltcore-dev/cortex/internal/conf"
	"github.com/cobaltcore-dev/cortex/internal/db"
	"github.com/cobaltcore-dev/cortex/internal/keystone"
	"github.com/cobaltcore-dev/cortex/internal/mqtt"
	"github.com/cobaltcore-dev/cortex/internal/sync"
	"github.com/cobaltcore-dev/cortex/internal/sync/openstack/identity"
	"github.com/cobaltcore-dev/cortex/internal/sync/openstack/limes"
	"github.com/cobaltcore-dev/cortex/internal/sync/openstack/manila"
	"github.com/cobaltcore-dev/cortex/internal/sync/openstack/nova"
	"github.com/cobaltcore-dev/cortex/internal/sync/openstack/placement"
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
	keystoneAPI keystone.KeystoneAPI,
	config conf.SyncOpenStackConfig,
	monitor sync.Monitor,
	db db.DB,
	mqttClient mqtt.Client,
) sync.Datasource {

	slog.Info("loading openstack sub-syncers")
	syncers := []Syncer{
		&nova.NovaSyncer{
			DB:         db,
			Mon:        monitor,
			Conf:       config.Nova,
			API:        nova.NewNovaAPI(monitor, keystoneAPI, config.Nova),
			MqttClient: mqttClient,
		},
		&placement.PlacementSyncer{
			DB:         db,
			Mon:        monitor,
			Conf:       config.Placement,
			API:        placement.NewPlacementAPI(monitor, keystoneAPI, config.Placement),
			MqttClient: mqttClient,
		},
		&manila.ManilaSyncer{
			DB:         db,
			Mon:        monitor,
			Conf:       config.Manila,
			API:        manila.NewManilaAPI(monitor, keystoneAPI, config.Manila),
			MqttClient: mqttClient,
		},
		&identity.IdentitySyncer{
			DB:         db,
			Mon:        monitor,
			API:        identity.NewIdentityAPI(monitor, keystoneAPI, config.Identity),
			MqttClient: mqttClient,
			Conf:       config.Identity,
		},
		&limes.LimesSyncer{
			DB:         db,
			Mon:        monitor,
			API:        limes.NewLimesAPI(monitor, keystoneAPI, config.Limes),
			MqttClient: mqttClient,
			Conf:       config.Limes,
		},
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
		go func(syncer Syncer) {
			defer wg.Done()
			if err := syncer.Sync(context); err != nil {
				slog.Error("failed to sync objects", "error", err)
			}
		}(syncer)
	}
	wg.Wait()
}
