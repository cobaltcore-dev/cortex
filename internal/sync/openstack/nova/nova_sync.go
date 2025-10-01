// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package nova

import (
	"context"
	"slices"
	"time"

	"github.com/cobaltcore-dev/cortex/internal/db"
	"github.com/cobaltcore-dev/cortex/internal/mqtt"
	"github.com/cobaltcore-dev/cortex/internal/sync"
	"github.com/go-gorp/gorp"
)

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
	tables := []*gorp.TableMap{}
	// Only add the tables that are configured in the yaml conf.
	if slices.Contains(s.Conf.Types, "servers") {
		tables = append(tables, s.DB.AddTable(Server{}))
	}
	if slices.Contains(s.Conf.Types, "deleted_servers") {
		tables = append(tables, s.DB.AddTable(DeletedServer{}))
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
		allServers, err := s.SyncAllServers(ctx)
		if err != nil {
			return err
		}
		if len(allServers) > 0 {
			go s.MqttClient.Publish(TriggerNovaServersSynced, "")
		}
	}
	if slices.Contains(s.Conf.Types, "deleted_servers") {
		deletedServers, err := s.SyncDeletedServers(ctx)
		if err != nil {
			return err
		}
		if len(deletedServers) > 0 {
			go s.MqttClient.Publish(TriggerNovaDeletedServersSynced, "")
		}
	}
	if slices.Contains(s.Conf.Types, "hypervisors") {
		allHypervisors, err := s.SyncAllHypervisors(ctx)
		if err != nil {
			return err
		}
		if len(allHypervisors) > 0 {
			go s.MqttClient.Publish(TriggerNovaHypervisorsSynced, "")
		}
	}
	if slices.Contains(s.Conf.Types, "flavors") {
		allFlavors, err := s.SyncAllFlavors(ctx)
		if err != nil {
			return err
		}
		if len(allFlavors) > 0 {
			go s.MqttClient.Publish(TriggerNovaFlavorsSynced, "")
		}
	}
	if slices.Contains(s.Conf.Types, "migrations") {
		allMigrations, err := s.SyncAllMigrations(ctx)
		if err != nil {
			return err
		}
		if len(allMigrations) > 0 {
			go s.MqttClient.Publish(TriggerNovaMigrationsSynced, "")
		}
	}
	if slices.Contains(s.Conf.Types, "aggregates") {
		allAggregates, err := s.SyncAllAggregates(ctx)
		if err != nil {
			return err
		}
		if len(allAggregates) > 0 {
			go s.MqttClient.Publish(TriggerNovaAggregatesSynced, "")
		}
	}
	return nil
}

// Sync all the active OpenStack servers into the database. (Includes ERROR, SHUTOFF, etc. state)
func (s *NovaSyncer) SyncAllServers(ctx context.Context) ([]Server, error) {
	allServers, err := s.API.GetAllServers(ctx)
	if err != nil {
		return nil, err
	}
	err = db.ReplaceAll(s.DB, allServers...)
	if err != nil {
		return nil, err
	}
	return allServers, nil
}

// Sync all the deleted OpenStack servers into the database.
// Only fetch servers that were deleted since the last sync run.
func (s *NovaSyncer) SyncDeletedServers(ctx context.Context) ([]DeletedServer, error) {
	// Default time frame is the last 6 hours
	since := time.Now().Add(-6 * time.Hour)

	// If there is a configured value, use that instead.
	if s.Conf.DeletedServersChangesSinceMinutes != nil {
		since = time.Now().Add(-time.Duration(*s.Conf.DeletedServersChangesSinceMinutes) * time.Minute)
	}

	deletedServers, err := s.API.GetDeletedServers(ctx, since)
	if err != nil {
		return nil, err
	}
	err = db.ReplaceAll(s.DB, deletedServers...)
	if err != nil {
		return nil, err
	}
	return deletedServers, nil
}

// Sync the OpenStack hypervisors into the database.
func (s *NovaSyncer) SyncAllHypervisors(ctx context.Context) ([]Hypervisor, error) {
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
func (s *NovaSyncer) SyncAllFlavors(ctx context.Context) ([]Flavor, error) {
	allFlavors, err := s.API.GetAllFlavors(ctx)
	if err != nil {
		return nil, err
	}
	err = db.ReplaceAll(s.DB, allFlavors...)
	if err != nil {
		return nil, err
	}
	return allFlavors, nil
}

// Sync the OpenStack migrations into the database.
func (s *NovaSyncer) SyncAllMigrations(ctx context.Context) ([]Migration, error) {
	allMigrations, err := s.API.GetAllMigrations(ctx)
	if err != nil {
		return nil, err
	}
	err = db.ReplaceAll(s.DB, allMigrations...)
	if err != nil {
		return nil, err
	}
	return allMigrations, nil
}

func (s *NovaSyncer) SyncAllAggregates(ctx context.Context) ([]Aggregate, error) {
	allAggregates, err := s.API.GetAllAggregates(ctx)
	if err != nil {
		return nil, err
	}
	err = db.ReplaceAll(s.DB, allAggregates...)
	if err != nil {
		return nil, err
	}
	return allAggregates, nil
}
