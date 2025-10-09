// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package placement

import (
	"context"
	"slices"

	"github.com/cobaltcore-dev/cortex/lib/db"
	"github.com/cobaltcore-dev/cortex/lib/mqtt"
	"github.com/cobaltcore-dev/cortex/sync/api/objects/openstack/placement"
	sync "github.com/cobaltcore-dev/cortex/sync/internal"
	"github.com/cobaltcore-dev/cortex/sync/internal/conf"
	"github.com/go-gorp/gorp"
)

// Syncer for OpenStack placement.
type PlacementSyncer struct {
	// Database to store the placement objects in.
	DB db.DB
	// Monitor to track the syncer.
	Mon sync.Monitor
	// Configuration for the placement syncer.
	Conf conf.SyncOpenStackPlacementConfig
	// Placement API client to fetch the data.
	API PlacementAPI
	// MQTT client to publish mqtt data.
	MqttClient mqtt.Client
}

// Init the OpenStack resource provider and trait syncer.
func (s *PlacementSyncer) Init(ctx context.Context) {
	s.API.Init(ctx)
	var tables = []*gorp.TableMap{}
	// Only add the tables that are configured in the yaml conf.
	if slices.Contains(s.Conf.Types, "resource_providers") {
		tables = append(tables, s.DB.AddTable(placement.ResourceProvider{}))
		// Depends on the resource providers. (Checked during conf validation)
		if slices.Contains(s.Conf.Types, "traits") {
			tables = append(tables, s.DB.AddTable(placement.Trait{}))
		}
		if slices.Contains(s.Conf.Types, "resource_providers") {
			tables = append(tables, s.DB.AddTable(placement.InventoryUsage{}))
		}
	}
	if err := s.DB.CreateTable(tables...); err != nil {
		panic(err)
	}
}

// Sync the OpenStack placement objects.
func (s *PlacementSyncer) Sync(ctx context.Context) error {
	// Only sync the objects that are configured in the yaml conf.
	if slices.Contains(s.Conf.Types, "resource_providers") {
		rps, err := s.SyncResourceProviders(ctx)
		if err != nil {
			return err
		}
		go s.MqttClient.Publish(placement.TriggerPlacementResourceProvidersSynced, "")
		// Dependencies of the resource providers.
		if slices.Contains(s.Conf.Types, "traits") {
			if _, err := s.SyncTraits(ctx, rps); err != nil {
				return err
			}
			go s.MqttClient.Publish(placement.TriggerPlacementTraitsSynced, "")
		}
		if slices.Contains(s.Conf.Types, "inventory_usages") {
			if _, err := s.SyncInventoryUsages(ctx, rps); err != nil {
				return err
			}
			go s.MqttClient.Publish(placement.TriggerPlacementInventoryUsagesSynced, "")
		}
	}
	return nil
}

// Sync the OpenStack resource providers into the database.
func (s *PlacementSyncer) SyncResourceProviders(ctx context.Context) ([]placement.ResourceProvider, error) {
	label := placement.ResourceProvider{}.TableName()
	rps, err := s.API.GetAllResourceProviders(ctx)
	if err != nil {
		return nil, err
	}
	if err := db.ReplaceAll(s.DB, rps...); err != nil {
		return nil, err
	}
	if s.Mon.PipelineObjectsGauge != nil {
		gauge := s.Mon.PipelineObjectsGauge.WithLabelValues(label)
		gauge.Set(float64(len(rps)))
	}
	if s.Mon.PipelineRequestProcessedCounter != nil {
		counter := s.Mon.PipelineRequestProcessedCounter.WithLabelValues(label)
		counter.Inc()
	}
	return rps, nil
}

// Sync the OpenStack traits into the database.
func (s *PlacementSyncer) SyncTraits(ctx context.Context, rps []placement.ResourceProvider) ([]placement.Trait, error) {
	label := placement.Trait{}.TableName()
	traits, err := s.API.GetAllTraits(ctx, rps)
	if err != nil {
		return nil, err
	}
	if err := db.ReplaceAll(s.DB, traits...); err != nil {
		return nil, err
	}
	if s.Mon.PipelineObjectsGauge != nil {
		gauge := s.Mon.PipelineObjectsGauge.WithLabelValues(label)
		gauge.Set(float64(len(traits)))
	}
	if s.Mon.PipelineRequestProcessedCounter != nil {
		counter := s.Mon.PipelineRequestProcessedCounter.WithLabelValues(label)
		counter.Inc()
	}
	return traits, err
}

// Sync the OpenStack resource provider inventories and usages into the database.
func (s *PlacementSyncer) SyncInventoryUsages(ctx context.Context, rps []placement.ResourceProvider) ([]placement.InventoryUsage, error) {
	label := placement.InventoryUsage{}.TableName()
	inventoryUsages, err := s.API.GetAllInventoryUsages(ctx, rps)
	if err != nil {
		return nil, err
	}
	if err := db.ReplaceAll(s.DB, inventoryUsages...); err != nil {
		return nil, err
	}
	if s.Mon.PipelineObjectsGauge != nil {
		gauge := s.Mon.PipelineObjectsGauge.WithLabelValues(label)
		gauge.Set(float64(len(inventoryUsages)))
	}
	if s.Mon.PipelineRequestProcessedCounter != nil {
		counter := s.Mon.PipelineRequestProcessedCounter.WithLabelValues(label)
		counter.Inc()
	}
	return inventoryUsages, err
}
