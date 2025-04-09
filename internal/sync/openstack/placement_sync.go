// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package openstack

import (
	"context"
	"slices"

	"github.com/cobaltcore-dev/cortex/internal/db"
	"github.com/cobaltcore-dev/cortex/internal/mqtt"
	"github.com/cobaltcore-dev/cortex/internal/sync"
	"github.com/go-gorp/gorp"
)

// Syncer for OpenStack placement.
type placementSyncer struct {
	// Database to store the placement objects in.
	db db.DB
	// Monitor to track the syncer.
	mon sync.Monitor
	// Configuration for the placement syncer.
	conf PlacementConf
	// Placement API client to fetch the data.
	api PlacementAPI
	// MQTT client to publish mqtt data.
	mqttClient mqtt.Client
}

// Create a new OpenStack placement syncer.
func newPlacementSyncer(db db.DB, mon sync.Monitor, k KeystoneAPI, conf PlacementConf) Syncer {
	return &placementSyncer{
		db:         db,
		mon:        mon,
		conf:       conf,
		api:        NewPlacementAPI(mon, k, conf),
		mqttClient: mqtt.NewClient(),
	}
}

// Init the OpenStack resource provider and trait syncer.
func (s *placementSyncer) Init(ctx context.Context) {
	s.api.Init(ctx)
	var tables = []*gorp.TableMap{}
	// Only add the tables that are configured in the yaml conf.
	if slices.Contains(s.conf.Types, "resource_providers") {
		tables = append(tables, s.db.AddTable(ResourceProvider{}))
		// Depends on the resource providers. (Checked during conf validation)
		if slices.Contains(s.conf.Types, "traits") {
			tables = append(tables, s.db.AddTable(Trait{}))
		}
	}
	if err := s.db.CreateTable(tables...); err != nil {
		panic(err)
	}
}

// Sync the OpenStack placement objects.
func (s *placementSyncer) Sync(ctx context.Context) error {
	// Only sync the objects that are configured in the yaml conf.
	if slices.Contains(s.conf.Types, "resource_providers") {
		rps, err := s.SyncResourceProviders(ctx)
		if err != nil {
			return err
		}
		go s.mqttClient.Publish(TriggerPlacementResourceProvidersSynced, "")
		// Dependencies of the resource providers.
		if slices.Contains(s.conf.Types, "traits") {
			if _, err := s.SyncTraits(ctx, rps); err != nil {
				return err
			}
			go s.mqttClient.Publish(TriggerPlacementTraitsSynced, "")
		}
	}
	return nil
}

// Sync the OpenStack resource providers into the database.
func (s *placementSyncer) SyncResourceProviders(ctx context.Context) ([]ResourceProvider, error) {
	label := ResourceProvider{}.TableName()
	rps, err := s.api.GetAllResourceProviders(ctx)
	if err != nil {
		return nil, err
	}
	if err := db.ReplaceAll(s.db, rps...); err != nil {
		return nil, err
	}
	if s.mon.PipelineObjectsGauge != nil {
		gauge := s.mon.PipelineObjectsGauge.WithLabelValues(label)
		gauge.Set(float64(len(rps)))
	}
	if s.mon.PipelineRequestProcessedCounter != nil {
		counter := s.mon.PipelineRequestProcessedCounter.WithLabelValues(label)
		counter.Inc()
	}
	return rps, nil
}

// Sync the OpenStack traits into the database.
func (s *placementSyncer) SyncTraits(ctx context.Context, rps []ResourceProvider) ([]Trait, error) {
	label := Trait{}.TableName()
	traits, err := s.api.GetAllTraits(ctx, rps)
	if err != nil {
		return nil, err
	}
	if err := db.ReplaceAll(s.db, traits...); err != nil {
		return nil, err
	}
	if s.mon.PipelineObjectsGauge != nil {
		gauge := s.mon.PipelineObjectsGauge.WithLabelValues(label)
		gauge.Set(float64(len(traits)))
	}
	if s.mon.PipelineRequestProcessedCounter != nil {
		counter := s.mon.PipelineRequestProcessedCounter.WithLabelValues(label)
		counter.Inc()
	}
	return traits, err
}
