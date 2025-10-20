// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package nova

import (
	"context"
	"slices"
	"time"

	"github.com/cobaltcore-dev/cortex/knowledge/api/datasources/openstack/nova"
	"github.com/cobaltcore-dev/cortex/knowledge/internal/conf"
	"github.com/cobaltcore-dev/cortex/knowledge/internal/datasources"
	"github.com/cobaltcore-dev/cortex/lib/db"
	"github.com/cobaltcore-dev/cortex/lib/mqtt"
	"github.com/go-gorp/gorp"
)

// Syncer for OpenStack nova.
type NovaSyncer struct {
	// Database to store the nova objects in.
	DB db.DB
	// Monitor to track the syncer.
	Mon datasources.Monitor
	// Configuration for the nova syncer.
	Conf conf.DatasourceOpenStackNovaConfig
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
		tables = append(tables, s.DB.AddTable(nova.Server{}))
	}
	if slices.Contains(s.Conf.Types, "deleted_servers") {
		tables = append(tables, s.DB.AddTable(nova.DeletedServer{}))
	}
	if slices.Contains(s.Conf.Types, "hypervisors") {
		tables = append(tables, s.DB.AddTable(nova.Hypervisor{}))
	}
	if slices.Contains(s.Conf.Types, "flavors") {
		tables = append(tables, s.DB.AddTable(nova.Flavor{}))
	}
	if slices.Contains(s.Conf.Types, "migrations") {
		tables = append(tables, s.DB.AddTable(nova.Migration{}))
	}
	if slices.Contains(s.Conf.Types, "aggregates") {
		tables = append(tables, s.DB.AddTable(nova.Aggregate{}))
	}
	if err := s.DB.CreateTable(tables...); err != nil {
		panic(err)
	}
}

// Sync the OpenStack nova objects and publish triggers.
func (s *NovaSyncer) Sync(ctx context.Context) error {
	// Only sync the objects that are configured in the yaml conf.
	if slices.Contains(s.Conf.Types, "servers") {
		_, err := s.SyncAllServers(ctx)
		if err != nil {
			return err
		}
		go s.MqttClient.Publish(nova.TriggerNovaServersSynced, "")
	}
	if slices.Contains(s.Conf.Types, "deleted_servers") {
		_, err := s.SyncDeletedServers(ctx)
		if err != nil {
			return err
		}
		go s.MqttClient.Publish(nova.TriggerNovaDeletedServersSynced, "")
	}
	if slices.Contains(s.Conf.Types, "hypervisors") {
		_, err := s.SyncAllHypervisors(ctx)
		if err != nil {
			return err
		}
		go s.MqttClient.Publish(nova.TriggerNovaHypervisorsSynced, "")
	}
	if slices.Contains(s.Conf.Types, "flavors") {
		_, err := s.SyncAllFlavors(ctx)
		if err != nil {
			return err
		}
		go s.MqttClient.Publish(nova.TriggerNovaFlavorsSynced, "")
	}
	if slices.Contains(s.Conf.Types, "migrations") {
		_, err := s.SyncAllMigrations(ctx)
		if err != nil {
			return err
		}
		go s.MqttClient.Publish(nova.TriggerNovaMigrationsSynced, "")
	}
	if slices.Contains(s.Conf.Types, "aggregates") {
		_, err := s.SyncAllAggregates(ctx)
		if err != nil {
			return err
		}
		go s.MqttClient.Publish(nova.TriggerNovaAggregatesSynced, "")
	}
	return nil
}

// Sync all the active OpenStack servers into the database. (Includes ERROR, SHUTOFF, etc. state)
func (s *NovaSyncer) SyncAllServers(ctx context.Context) ([]nova.Server, error) {
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
func (s *NovaSyncer) SyncDeletedServers(ctx context.Context) ([]nova.DeletedServer, error) {
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
func (s *NovaSyncer) SyncAllHypervisors(ctx context.Context) ([]nova.Hypervisor, error) {
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
func (s *NovaSyncer) SyncAllFlavors(ctx context.Context) ([]nova.Flavor, error) {
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
func (s *NovaSyncer) SyncAllMigrations(ctx context.Context) ([]nova.Migration, error) {
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

// Sync the OpenStack aggregates into the database.
func (s *NovaSyncer) SyncAllAggregates(ctx context.Context) ([]nova.Aggregate, error) {
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
