// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package openstack

import (
	"context"
	"slices"

	"github.com/cobaltcore-dev/cortex/internal/db"
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
}

// Create a new OpenStack nova syncer.
func newNovaSyncer(db db.DB, mon sync.Monitor, k KeystoneAPI, conf NovaConf) Syncer {
	return &novaSyncer{
		db:   db,
		mon:  mon,
		conf: conf,
		api:  newNovaAPI(mon, k, conf),
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
	servers, err := s.api.GetAllServers(ctx)
	if err != nil {
		return nil, err
	}
	if err := db.ReplaceAll(s.db, servers...); err != nil {
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
func (s *novaSyncer) SyncFlavors(ctx context.Context) ([]Flavor, error) {
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
