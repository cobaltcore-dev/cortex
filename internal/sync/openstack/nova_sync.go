// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package openstack

import (
	"context"
	"log/slog"
	"slices"

	"github.com/cobaltcore-dev/cortex/internal/db"
	"github.com/cobaltcore-dev/cortex/internal/mqtt"
	"github.com/cobaltcore-dev/cortex/internal/sync"
	"github.com/go-gorp/gorp"
)

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
	tables := []*gorp.TableMap{}
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
		newServers, err := s.SyncNewServers(ctx)
		if err != nil {
			return err
		}
		if len(newServers) > 0 {
			go s.mqttClient.Publish(TriggerNovaServersSynced, "")
		}
	}
	if slices.Contains(s.conf.Types, "hypervisors") {
		allHypervisors, err := s.SyncAllHypervisors(ctx)
		if err != nil {
			return err
		}
		go s.mqttClient.Publish(TriggerNovaHypervisorsSynced, "")
		// Publish additional information required for the visualizer.
		go s.mqttClient.Publish("cortex/sync/openstack/nova/hypervisors", allHypervisors)
	}
	if slices.Contains(s.conf.Types, "flavors") {
		_, err := s.SyncAllFlavors(ctx)
		if err != nil {
			return err
		}
		go s.mqttClient.Publish(TriggerNovaFlavorsSynced, "")
	}
	return nil
}

// Sync the OpenStack servers into the database.
// Return only new servers that were created since the last sync.
func (s *novaSyncer) SyncNewServers(ctx context.Context) ([]Server, error) {
	tableName := Server{}.TableName()
	// Only fetch servers that were created since the last one.
	var lastSyncTime *string
	var mostRecentServer Server
	q := "SELECT * FROM " + tableName + " ORDER BY created DESC LIMIT 1"
	if err := s.db.SelectOne(&mostRecentServer, q); err == nil {
		lastSyncTime = &mostRecentServer.Created
		slog.Info("last server fetched", "time", lastSyncTime)
	} else {
		slog.Info("no previous server fetched")
	}
	newServers, err := s.api.GetNewServers(ctx, lastSyncTime)
	if err != nil {
		return nil, err
	}
	// Check if the servers are already in the database.
	var nServersInDB int
	q = "SELECT COUNT(*) FROM " + tableName
	if err := s.db.SelectOne(&nServersInDB, q); err != nil {
		return nil, err
	}
	var existingServers []Server
	if nServersInDB > 0 && len(newServers) > 0 {
		// Check which servers only need to be updated instead of inserted.
		// Using a contains query with the server ID:
		q = "SELECT id FROM " + tableName + " WHERE id IN ("
		for i, server := range newServers {
			if i > 0 {
				q += ", "
			}
			q += "'" + server.ID + "'"
		}
		q += ")"
		if _, err := s.db.Select(&existingServers, q); err != nil {
			return nil, err
		}
	}
	existingServersByID := make(map[string]Server, len(existingServers))
	for _, server := range existingServers {
		existingServersByID[server.ID] = server
	}
	tx, err := s.db.Begin()
	if err != nil {
		return nil, err
	}
	for _, server := range newServers {
		if _, ok := existingServersByID[server.ID]; ok {
			if _, err := tx.Update(&server); err != nil {
				return nil, tx.Rollback()
			}
		} else {
			if err := tx.Insert(&server); err != nil {
				return nil, tx.Rollback()
			}
		}
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	// Delete servers that have a DELETED status.
	q = "DELETE FROM " + Server{}.TableName() + " WHERE status = 'DELETED'"
	if _, err := s.db.Exec(q); err != nil {
		return nil, err
	}
	// Check how many servers we have in the database.
	q = "SELECT COUNT(*) FROM " + tableName
	var count int
	if err := s.db.SelectOne(&count, q); err != nil {
		return nil, err
	}
	if s.mon.PipelineObjectsGauge != nil {
		gauge := s.mon.PipelineObjectsGauge.WithLabelValues(tableName)
		gauge.Set(float64(count))
	}
	if s.mon.PipelineRequestProcessedCounter != nil {
		counter := s.mon.PipelineRequestProcessedCounter.WithLabelValues(tableName)
		counter.Inc()
	}
	return newServers, nil
}

// Sync the OpenStack hypervisors into the database.
func (s *novaSyncer) SyncAllHypervisors(ctx context.Context) ([]Hypervisor, error) {
	label := Hypervisor{}.TableName()
	hypervisors, err := s.api.GetAllHypervisors(ctx)
	if err != nil {
		return nil, err
	}
	if err := db.ReplaceAll(s.db, hypervisors...); err != nil {
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
func (s *novaSyncer) SyncAllFlavors(ctx context.Context) ([]Flavor, error) {
	label := Flavor{}.TableName()
	flavors, err := s.api.GetAllFlavors(ctx)
	if err != nil {
		return nil, err
	}
	if err := db.ReplaceAll(s.db, flavors...); err != nil {
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
