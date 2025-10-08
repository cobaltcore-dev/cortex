// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package cinder

import (
	"context"
	"slices"

	"github.com/cobaltcore-dev/cortex/internal/db"
	"github.com/cobaltcore-dev/cortex/internal/mqtt"
	"github.com/cobaltcore-dev/cortex/sync/api/objects/openstack/cinder"
	sync "github.com/cobaltcore-dev/cortex/sync/internal"
	"github.com/cobaltcore-dev/cortex/sync/internal/conf"
	"github.com/go-gorp/gorp"
)

type CinderSyncer struct {
	// Database to store the manila objects in.
	DB db.DB
	// Monitor to track the syncer.
	Mon sync.Monitor
	// Configuration for the cinder syncer.
	Conf conf.SyncOpenStackCinderConfig
	// Cinder API client to fetch the data.
	API CinderAPI
	// MQTT client to publish mqtt data.
	MqttClient mqtt.Client
}

// Init the OpenStack cinder syncer.
func (s *CinderSyncer) Init(ctx context.Context) {
	s.API.Init(ctx)
	tables := []*gorp.TableMap{}
	// Only add the tables that are configured in the yaml conf.
	if slices.Contains(s.Conf.Types, "storage_pools") {
		tables = append(tables, s.DB.AddTable(cinder.StoragePool{}))
	}
	if err := s.DB.CreateTable(tables...); err != nil {
		panic(err)
	}
}

// Sync the OpenStack cinder objects and publish triggers.
func (s *CinderSyncer) Sync(ctx context.Context) error {
	// Only sync the objects that are configured in the yaml conf.
	if slices.Contains(s.Conf.Types, "storage_pools") {
		changedPools, err := s.SyncAllStoragePools(ctx)
		if err != nil {
			return err
		}
		go s.MqttClient.Publish(cinder.TriggerCinderStoragePoolsSynced, "")
		// Publish additional information required for the visualizer.
		go s.MqttClient.Publish("cortex/sync/openstack/cinder/storage_pools", changedPools)
	}
	return nil
}

// Sync the OpenStack resource providers into the database.
func (s *CinderSyncer) SyncAllStoragePools(ctx context.Context) ([]cinder.StoragePool, error) {
	label := cinder.StoragePool{}.TableName()
	pools, err := s.API.GetAllStoragePools(ctx)
	if err != nil {
		return nil, err
	}
	if err := db.ReplaceAll(s.DB, pools...); err != nil {
		return nil, err
	}
	if s.Mon.PipelineObjectsGauge != nil {
		gauge := s.Mon.PipelineObjectsGauge.WithLabelValues(label)
		gauge.Set(float64(len(pools)))
	}
	if s.Mon.PipelineRequestProcessedCounter != nil {
		counter := s.Mon.PipelineRequestProcessedCounter.WithLabelValues(label)
		counter.Inc()
	}
	return pools, nil
}
