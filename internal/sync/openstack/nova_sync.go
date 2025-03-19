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
	// Only initialized if mqtt is enabled.
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

// Sync the OpenStack nova objects.
func (s *novaSyncer) Sync(ctx context.Context) error {
	// Only sync the objects that are configured in the yaml conf.
	if slices.Contains(s.conf.Types, "servers") {
		if _, err := s.SyncServers(ctx); err != nil {
			return err
		}
	}
	if slices.Contains(s.conf.Types, "hypervisors") {
		if _, err := s.SyncHypervisors(ctx); err != nil {
			return err
		}
	}
	if slices.Contains(s.conf.Types, "flavors") {
		if _, err := s.SyncFlavors(ctx); err != nil {
			return err
		}
	}
	return nil
}

// Sync the OpenStack servers into the database.
func (s *novaSyncer) SyncServers(ctx context.Context) ([]Server, error) {
	label := Server{}.TableName()
	// Check when the last sync run was performed, if there was one.
	lastSyncTime := time.Time{} // Default to zero time.
	var lastSync novaSync
	if err := s.db.SelectOne(&lastSync, `
		SELECT * FROM nova_sync WHERE name = :name ORDER BY time DESC LIMIT 1
	`, map[string]any{"name": label}); err == nil {
		lastSyncTime = lastSync.Time
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
	// Publish information about the servers to an mqtt broker.
	// In this way, the changes in server placement etc. can be monitored
	// or visualized by other services.
	if s.mqttClient != nil {
		//nolint:errcheck // Don't need to check the error here. It should be logged.
		go s.mqttClient.Publish("cortex/sync/openstack/nova/servers", servers)
	}
	return servers, nil
}

// Sync the OpenStack hypervisors into the database.
func (s *novaSyncer) SyncHypervisors(ctx context.Context) ([]Hypervisor, error) {
	label := Hypervisor{}.TableName()
	// Check when the last sync run was performed, if there was one.
	lastSyncTime := time.Time{} // Default to zero time.
	var lastSync novaSync
	if err := s.db.SelectOne(&lastSync, `
		SELECT * FROM nova_sync WHERE name = :name ORDER BY time DESC LIMIT 1
	`, map[string]any{"name": label}); err == nil {
		lastSyncTime = lastSync.Time
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
	// Publish information about the hypervisors to an mqtt broker.
	// In this way, the changes in hypervisor usage etc. can be monitored
	// or visualized by other services.
	if s.mqttClient != nil {
		//nolint:errcheck // Don't need to check the error here. It should be logged.
		go s.mqttClient.Publish("cortex/sync/openstack/nova/hypervisors", hypervisors)
	}
	return hypervisors, nil
}

// Sync the OpenStack flavors into the database.
func (s *novaSyncer) SyncFlavors(ctx context.Context) ([]Flavor, error) {
	label := Flavor{}.TableName()
	// Check when the last sync run was performed, if there was one.
	lastSyncTime := time.Time{} // Default to zero time.
	var lastSync novaSync
	if err := s.db.SelectOne(&lastSync, `
		SELECT * FROM nova_sync WHERE name = :name ORDER BY time DESC LIMIT 1
	`, map[string]any{"name": label}); err == nil {
		lastSyncTime = lastSync.Time
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
