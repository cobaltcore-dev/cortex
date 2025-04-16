// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package openstack

import (
	"context"
	"log/slog"
	"slices"
	"strconv"
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
	if slices.Contains(s.conf.Types, "migrations") {
		tables = append(tables, s.db.AddTable(Migration{}))
	}
	if err := s.db.CreateTable(tables...); err != nil {
		panic(err)
	}
}

// Sync the OpenStack nova objects and publish triggers.
func (s *novaSyncer) Sync(ctx context.Context) error {
	// Only sync the objects that are configured in the yaml conf.
	if slices.Contains(s.conf.Types, "servers") {
		changedServers, err := s.SyncChangedServers(ctx)
		if err != nil {
			return err
		}
		if len(changedServers) > 0 {
			go s.mqttClient.Publish(TriggerNovaServersSynced, "")
		}
	}
	if slices.Contains(s.conf.Types, "hypervisors") {
		changedHypervisors, err := s.SyncChangedHypervisors(ctx)
		if err != nil {
			return err
		}
		go s.mqttClient.Publish(TriggerNovaHypervisorsSynced, "")
		// Publish additional information required for the visualizer.
		go s.mqttClient.Publish("cortex/sync/openstack/nova/hypervisors", changedHypervisors)
	}
	if slices.Contains(s.conf.Types, "flavors") {
		changedFlavors, err := s.SyncChangedFlavors(ctx)
		if err != nil {
			return err
		}
		if len(changedFlavors) > 0 {
			go s.mqttClient.Publish(TriggerNovaFlavorsSynced, "")
		}
	}
	if slices.Contains(s.conf.Types, "migrations") {
		changedMigrations, err := s.SyncChangedMigrations(ctx)
		if err != nil {
			return err
		}
		if len(changedMigrations) > 0 {
			go s.mqttClient.Publish(TriggerNovaMigrationsSynced, "")
		}
	}
	return nil
}

// Check when the last sync run for a specific table was performed.
// If there was no sync run, return nil.
func (s *novaSyncer) getLastSyncTime(tableName string) *time.Time {
	// Check when the last sync run was performed, if there was one.
	var lastSyncTime *time.Time
	var lastSync novaSync
	if err := s.db.SelectOne(&lastSync, `
		SELECT * FROM nova_sync WHERE name = :name ORDER BY time DESC LIMIT 1
	`, map[string]any{"name": tableName}); err == nil {
		lastSyncTime = &lastSync.Time
		slog.Info("last nova sync run", "time", lastSync.Time, "table", tableName)
	} else {
		slog.Info("no previous nova sync run", "table", tableName)
	}
	return lastSyncTime
}

// Store a new sync run in the database.
func (s *novaSyncer) setLastSyncTime(tableName string, time time.Time) {
	if err := s.db.Insert(&novaSync{Name: tableName, Time: time}); err != nil {
		slog.Error("failed to insert nova sync", "error", err)
	}
}

// Upsert nova objects into the database.
func upsert[O any](s *novaSyncer, objects []O, pk string, getpk func(O) string, tableName string) error {
	nObjectsInDB := 0
	q := "SELECT COUNT(*) FROM " + tableName
	if err := s.db.SelectOne(&nObjectsInDB, q); err != nil {
		return err
	}
	var existingObjects []O
	if nObjectsInDB > 0 && len(objects) > 0 {
		// Check which objects only need to be updated instead of inserted.
		// Using a contains query with the object ID:
		q = "SELECT " + pk + " FROM " + tableName + " WHERE " + pk + " IN ("
		for i, object := range objects {
			if i > 0 {
				q += ", "
			}
			q += "'" + getpk(object) + "'"
		}
		q += ")"
		if _, err := s.db.Select(&existingObjects, q); err != nil {
			return err
		}
	}
	existingObjectsByID := make(map[string]O, len(existingObjects))
	for _, object := range existingObjects {
		existingObjectsByID[getpk(object)] = object
	}
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	for _, object := range objects {
		if _, ok := existingObjectsByID[getpk(object)]; ok {
			if _, err := tx.Update(&object); err != nil {
				return tx.Rollback()
			}
		} else {
			if err := tx.Insert(&object); err != nil {
				return tx.Rollback()
			}
		}
	}
	if err := tx.Commit(); err != nil {
		slog.Error("failed to commit transaction", "error", err)
	}
	// Check how many objects we have in the database.
	q = "SELECT COUNT(*) FROM " + tableName
	var count int
	if err := s.db.SelectOne(&count, q); err != nil {
		return err
	}
	if s.mon.PipelineObjectsGauge != nil {
		gauge := s.mon.PipelineObjectsGauge.WithLabelValues(tableName)
		gauge.Set(float64(count))
	}
	if s.mon.PipelineRequestProcessedCounter != nil {
		counter := s.mon.PipelineRequestProcessedCounter.WithLabelValues(tableName)
		counter.Inc()
	}
	return nil
}

// Sync the OpenStack servers into the database.
// Return only new servers that were created since the last sync.
func (s *novaSyncer) SyncChangedServers(ctx context.Context) ([]Server, error) {
	tableName := Server{}.TableName()
	lastSyncTime := s.getLastSyncTime(tableName)
	defer s.setLastSyncTime(tableName, time.Now())
	changedServers, err := s.api.GetChangedServers(ctx, lastSyncTime)
	if err != nil {
		return nil, err
	}
	err = upsert(s, changedServers, "id", func(s Server) string { return s.ID }, tableName)
	if err != nil {
		return nil, err
	}
	return changedServers, nil
}

// Sync the OpenStack hypervisors into the database.
func (s *novaSyncer) SyncChangedHypervisors(ctx context.Context) ([]Hypervisor, error) {
	tableName := Hypervisor{}.TableName()
	lastSyncTime := s.getLastSyncTime(tableName)
	defer s.setLastSyncTime(tableName, time.Now())
	changedHypervisors, err := s.api.GetChangedHypervisors(ctx, lastSyncTime)
	if err != nil {
		return nil, err
	}
	err = upsert(s, changedHypervisors, "id", func(h Hypervisor) string { return strconv.Itoa(h.ID) }, tableName)
	if err != nil {
		return nil, err
	}
	return changedHypervisors, nil
}

// Sync the OpenStack flavors into the database.
func (s *novaSyncer) SyncChangedFlavors(ctx context.Context) ([]Flavor, error) {
	tableName := Flavor{}.TableName()
	lastSyncTime := s.getLastSyncTime(tableName)
	defer s.setLastSyncTime(tableName, time.Now())
	changedFlavors, err := s.api.GetChangedFlavors(ctx, lastSyncTime)
	if err != nil {
		return nil, err
	}
	err = upsert(s, changedFlavors, "id", func(f Flavor) string { return f.ID }, tableName)
	if err != nil {
		return nil, err
	}
	return changedFlavors, nil
}

// Sync the OpenStack migrations into the database.
func (s *novaSyncer) SyncChangedMigrations(ctx context.Context) ([]Migration, error) {
	tableName := Migration{}.TableName()
	lastSyncTime := s.getLastSyncTime(tableName)
	defer s.setLastSyncTime(tableName, time.Now())
	changedMigrations, err := s.api.GetChangedMigrations(ctx, lastSyncTime)
	if err != nil {
		return nil, err
	}
	err = upsert(s, changedMigrations, "uuid", func(m Migration) string { return m.UUID }, tableName)
	if err != nil {
		return nil, err
	}
	return changedMigrations, nil
}
