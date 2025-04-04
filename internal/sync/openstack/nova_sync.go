// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package openstack

import (
	"context"
	"log/slog"
	"slices"
	"time"

	"github.com/cobaltcore-dev/cortex/internal/db"
	"github.com/cobaltcore-dev/cortex/internal/mqtt"
	"github.com/cobaltcore-dev/cortex/internal/sync"
	"github.com/go-gorp/gorp"
)

// Table to store which sync runs where performed and when.
type novaSync struct {
	// Name of the sync run.
	Name string `db:"name"`
	// Time when the sync run was performed.
	Time time.Time `db:"time"`
}

// Table under which the nova sync will be stored.
func (novaSync) TableName() string {
	return "nova_sync"
}

// Syncer for OpenStack nova.
type novaSyncer struct {
	// Database to store the nova objects in.
	db db.DB
	// Monitor to track the syncer.
	mon sync.Monitor
	// Configuration for the nova syncer.
	conf NovaConf
	// Nova API client to fetch the data.
	api NovaAPI
	// MQTT client to publish mqtt data.
	mqttClient mqtt.Client
}

// Create a new OpenStack nova syncer.
func newNovaSyncer(db db.DB, mon sync.Monitor, k KeystoneAPI, conf NovaConf) Syncer {
	return &novaSyncer{
		db:         db,
		mon:        mon,
		conf:       conf,
		api:        newNovaAPI(mon, k, conf),
		mqttClient: mqtt.NewClient(),
	}
}

// Init the OpenStack nova syncer.
func (s *novaSyncer) Init(ctx context.Context) {
	s.api.Init(ctx)
	tables := []*gorp.TableMap{s.db.AddTable(novaSync{})}
	// Only add the tables that are configured in the yaml conf.
	if slices.Contains(s.conf.Types, "servers") {
		tables = append(tables, s.db.AddTable(Server{}))
	}
	if slices.Contains(s.conf.Types, "hypervisors") {
		tables = append(tables, s.db.AddTable(Hypervisor{}))
	}
	if slices.Contains(s.conf.Types, "flavors") {
		tables = append(tables, s.db.AddTable(Flavor{}))
	}
	if err := s.db.CreateTable(tables...); err != nil {
		panic(err)
	}
}

// Sync the OpenStack nova objects and publish triggers.
func (s *novaSyncer) Sync(ctx context.Context) error {
	// Only sync the objects that are configured in the yaml conf.
	if slices.Contains(s.conf.Types, "servers") {
		newServers, err := s.SyncServers(ctx)
		if err != nil {
			return err
		}
		if len(newServers) > 0 {
			go s.mqttClient.Publish(TriggerNovaServersSynced, "")
		}
	}
	if slices.Contains(s.conf.Types, "hypervisors") {
		newHypervisors, err := s.SyncHypervisors(ctx)
		if err != nil {
			return err
		}
		if len(newHypervisors) > 0 {
			go s.mqttClient.Publish(TriggerNovaHypervisorsSynced, "")
			// Publish additional information required for the visualizer.
			go s.mqttClient.Publish("cortex/sync/openstack/nova/hypervisors", newHypervisors)
		}
	}
	if slices.Contains(s.conf.Types, "flavors") {
		newFlavors, err := s.SyncFlavors(ctx)
		if err != nil {
			return err
		}
		if len(newFlavors) > 0 {
			go s.mqttClient.Publish(TriggerNovaFlavorsSynced, "")
		}
	}
	return nil
}

// Sync the OpenStack servers into the database.
func (s *novaSyncer) SyncServers(ctx context.Context) ([]Server, error) {
	label := Server{}.TableName()
	// Check when the last sync run was performed, if there was one.
	var lastSyncTime *time.Time
	var lastSync novaSync
	if err := s.db.SelectOne(&lastSync, `
		SELECT * FROM nova_sync WHERE name = :name ORDER BY time DESC LIMIT 1
	`, map[string]any{"name": label}); err == nil {
		lastSyncTime = &lastSync.Time
		slog.Info("last nova sync run", "time", lastSync.Time)
	} else {
		slog.Info("no previous nova sync run")
	}
	servers, err := s.api.GetAllServers(ctx, lastSyncTime)
	if err != nil {
		return nil, err
	}
	if err := db.ReplaceAll(s.db, servers...); err != nil {
		return nil, err
	}
	// Store a sync run in the database to not fetch all servers again.
	if err := s.db.Insert(&novaSync{Name: label, Time: time.Now()}); err != nil {
		return nil, err
	}
	if s.mon.PipelineObjectsGauge != nil {
		gauge := s.mon.PipelineObjectsGauge.WithLabelValues(label)
		gauge.Set(float64(len(servers)))
	}
	if s.mon.PipelineRequestProcessedCounter != nil {
		counter := s.mon.PipelineRequestProcessedCounter.WithLabelValues(label)
		counter.Inc()
	}
	return servers, nil
}

// Sync the OpenStack hypervisors into the database.
func (s *novaSyncer) SyncHypervisors(ctx context.Context) ([]Hypervisor, error) {
	label := Hypervisor{}.TableName()
	// Check when the last sync run was performed, if there was one.
	var lastSyncTime *time.Time
	var lastSync novaSync
	if err := s.db.SelectOne(&lastSync, `
		SELECT * FROM nova_sync WHERE name = :name ORDER BY time DESC LIMIT 1
	`, map[string]any{"name": label}); err == nil {
		lastSyncTime = &lastSync.Time
		slog.Info("last nova sync run", "time", lastSync.Time)
	} else {
		slog.Info("no previous nova sync run")
	}
	hypervisors, err := s.api.GetAllHypervisors(ctx, lastSyncTime)
	if err != nil {
		return nil, err
	}
	if err := db.ReplaceAll(s.db, hypervisors...); err != nil {
		return nil, err
	}
	// Store a sync run in the database to not fetch all servers again.
	if err := s.db.Insert(&novaSync{Name: label, Time: time.Now()}); err != nil {
		return nil, err
	}
	if s.mon.PipelineObjectsGauge != nil {
		gauge := s.mon.PipelineObjectsGauge.WithLabelValues(label)
		gauge.Set(float64(len(hypervisors)))
	}
	if s.mon.PipelineRequestProcessedCounter != nil {
		counter := s.mon.PipelineRequestProcessedCounter.WithLabelValues(label)
		counter.Inc()
	}
	return hypervisors, nil
}

// Sync the OpenStack flavors into the database.
func (s *novaSyncer) SyncFlavors(ctx context.Context) ([]Flavor, error) {
	label := Flavor{}.TableName()
	// Check when the last sync run was performed, if there was one.
	var lastSyncTime *time.Time // Default to zero time.
	var lastSync novaSync
	if err := s.db.SelectOne(&lastSync, `
		SELECT * FROM nova_sync WHERE name = :name ORDER BY time DESC LIMIT 1
	`, map[string]any{"name": label}); err == nil {
		lastSyncTime = &lastSync.Time
		slog.Info("last nova sync run", "time", lastSync.Time)
	} else {
		slog.Info("no previous nova sync run")
	}
	flavors, err := s.api.GetAllFlavors(ctx, lastSyncTime)
	if err != nil {
		return nil, err
	}
	if err := db.ReplaceAll(s.db, flavors...); err != nil {
		return nil, err
	}
	// Store a sync run in the database to not fetch all servers again.
	if err := s.db.Insert(&novaSync{Name: label, Time: time.Now()}); err != nil {
		return nil, err
	}
	if s.mon.PipelineObjectsGauge != nil {
		gauge := s.mon.PipelineObjectsGauge.WithLabelValues(label)
		gauge.Set(float64(len(flavors)))
	}
	if s.mon.PipelineRequestProcessedCounter != nil {
		counter := s.mon.PipelineRequestProcessedCounter.WithLabelValues(label)
		counter.Inc()
	}
	return flavors, nil
}
