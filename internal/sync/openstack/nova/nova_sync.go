// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package nova

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

// Indexes for the nova sync table.
func (novaSync) Indexes() []db.Index {
	return nil
}

// Syncer for OpenStack nova.
type NovaSyncer struct {
	// Database to store the nova objects in.
	DB db.DB
	// Monitor to track the syncer.
	Mon sync.Monitor
	// Configuration for the nova syncer.
	Conf NovaConf
	// Nova API client to fetch the data.
	API NovaAPI
	// MQTT client to publish mqtt data.
	MqttClient mqtt.Client
}

// Init the OpenStack nova syncer.
func (s *NovaSyncer) Init(ctx context.Context) {
	s.API.Init(ctx)
	tables := []*gorp.TableMap{s.DB.AddTable(novaSync{})}
	// Only add the tables that are configured in the yaml conf.
	if slices.Contains(s.Conf.Types, "servers") {
		tables = append(tables, s.DB.AddTable(Server{}))
	}
	if slices.Contains(s.Conf.Types, "hypervisors") {
		tables = append(tables, s.DB.AddTable(Hypervisor{}))
	}
	if slices.Contains(s.Conf.Types, "flavors") {
		tables = append(tables, s.DB.AddTable(Flavor{}))
	}
	if slices.Contains(s.Conf.Types, "migrations") {
		tables = append(tables, s.DB.AddTable(Migration{}))
	}
	if slices.Contains(s.Conf.Types, "aggregates") {
		tables = append(tables, s.DB.AddTable(Aggregate{}))
	}
	if err := s.DB.CreateTable(tables...); err != nil {
		panic(err)
	}
}

// Sync the OpenStack nova objects and publish triggers.
func (s *NovaSyncer) Sync(ctx context.Context) error {
	// Only sync the objects that are configured in the yaml conf.
	if slices.Contains(s.Conf.Types, "servers") {
		changedServers, err := s.SyncChangedServers(ctx)
		if err != nil {
			return err
		}
		if len(changedServers) > 0 {
			go s.MqttClient.Publish(TriggerNovaServersSynced, "")
		}
	}
	if slices.Contains(s.Conf.Types, "hypervisors") {
		_, err := s.SyncChangedHypervisors(ctx)
		if err != nil {
			return err
		}
		go s.MqttClient.Publish(TriggerNovaHypervisorsSynced, "")
	}
	if slices.Contains(s.Conf.Types, "flavors") {
		changedFlavors, err := s.SyncChangedFlavors(ctx)
		if err != nil {
			return err
		}
		if len(changedFlavors) > 0 {
			go s.MqttClient.Publish(TriggerNovaFlavorsSynced, "")
		}
	}
	if slices.Contains(s.Conf.Types, "migrations") {
		changedMigrations, err := s.SyncChangedMigrations(ctx)
		if err != nil {
			return err
		}
		if len(changedMigrations) > 0 {
			go s.MqttClient.Publish(TriggerNovaMigrationsSynced, "")
		}
	}
	if slices.Contains(s.Conf.Types, "aggregates") {
		changedAggregates, err := s.SyncAllAggregates(ctx)
		if err != nil {
			return err
		}
		if len(changedAggregates) > 0 {
			go s.MqttClient.Publish(TriggerNovaAggregatesSynced, "")
		}
	}
	return nil
}

// Check when the last sync run for a specific table was performed.
// If there was no sync run, return nil.
func (s *NovaSyncer) getLastSyncTime(tableName string) *time.Time {
	// Check when the last sync run was performed, if there was one.
	var lastSyncTime *time.Time
	var lastSync novaSync
	if err := s.DB.SelectOne(&lastSync, `
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
func (s *NovaSyncer) setLastSyncTime(tableName string, time time.Time) {
	if err := s.DB.Insert(&novaSync{Name: tableName, Time: time}); err != nil {
		slog.Error("failed to insert nova sync", "error", err)
	}
}

// Upsert nova objects into the database.
func upsert[O any](s *NovaSyncer, objects []O, pk string, getpk func(O) string, tableName string) error {
	nObjectsInDB := 0
	q := "SELECT COUNT(*) FROM " + tableName
	if err := s.DB.SelectOne(&nObjectsInDB, q); err != nil {
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
		if _, err := s.DB.Select(&existingObjects, q); err != nil {
			return err
		}
	}
	existingObjectsByID := make(map[string]O, len(existingObjects))
	for _, object := range existingObjects {
		existingObjectsByID[getpk(object)] = object
	}
	tx, err := s.DB.Begin()
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
	if err := s.DB.SelectOne(&count, q); err != nil {
		return err
	}
	if s.Mon.PipelineObjectsGauge != nil {
		gauge := s.Mon.PipelineObjectsGauge.WithLabelValues(tableName)
		gauge.Set(float64(count))
	}
	if s.Mon.PipelineRequestProcessedCounter != nil {
		counter := s.Mon.PipelineRequestProcessedCounter.WithLabelValues(tableName)
		counter.Inc()
	}
	return nil
}

// Sync the OpenStack servers into the database.
// Return only new servers that were created since the last sync.
func (s *NovaSyncer) SyncChangedServers(ctx context.Context) ([]Server, error) {
	tableName := Server{}.TableName()
	lastSyncTime := s.getLastSyncTime(tableName)
	defer s.setLastSyncTime(tableName, time.Now())
	changedServers, err := s.API.GetChangedServers(ctx, lastSyncTime)
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
func (s *NovaSyncer) SyncChangedHypervisors(ctx context.Context) ([]Hypervisor, error) {
	allHypervisors, err := s.API.GetAllHypervisors(ctx)
	if err != nil {
		return nil, err
	}
	// Since the nova api doesn't support only returning changed
	// hypervisors, we can just replace all hypervisors in the database.
	err = db.ReplaceAll(s.DB, allHypervisors...)
	if err != nil {
		return nil, err
	}
	return allHypervisors, nil
}

// Sync the OpenStack flavors into the database.
func (s *NovaSyncer) SyncChangedFlavors(ctx context.Context) ([]Flavor, error) {
	tableName := Flavor{}.TableName()
	lastSyncTime := s.getLastSyncTime(tableName)
	defer s.setLastSyncTime(tableName, time.Now())
	changedFlavors, err := s.API.GetChangedFlavors(ctx, lastSyncTime)
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
func (s *NovaSyncer) SyncChangedMigrations(ctx context.Context) ([]Migration, error) {
	tableName := Migration{}.TableName()
	lastSyncTime := s.getLastSyncTime(tableName)
	defer s.setLastSyncTime(tableName, time.Now())
	changedMigrations, err := s.API.GetChangedMigrations(ctx, lastSyncTime)
	if err != nil {
		return nil, err
	}
	err = upsert(s, changedMigrations, "uuid", func(m Migration) string { return m.UUID }, tableName)
	if err != nil {
		return nil, err
	}
	return changedMigrations, nil
}

func (s *NovaSyncer) SyncAllAggregates(ctx context.Context) ([]Aggregate, error) {
	changedAggregates, err := s.API.GetAllAggregates(ctx)
	if err != nil {
		return nil, err
	}
	err = db.ReplaceAll(s.DB, changedAggregates...)
	if err != nil {
		return nil, err
	}
	return changedAggregates, nil
}
