// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package nova

import (
	"context"
	"time"

	"github.com/cobaltcore-dev/cortex/knowledge/api/datasources/openstack/nova"
	"github.com/cobaltcore-dev/cortex/knowledge/api/v1alpha1"
	"github.com/cobaltcore-dev/cortex/knowledge/internal/datasources"
	"github.com/cobaltcore-dev/cortex/lib/db"
	"github.com/go-gorp/gorp"
)

// Syncer for OpenStack nova.
type NovaSyncer struct {
	// Database to store the nova objects in.
	DB db.DB
	// Monitor to track the syncer.
	Mon datasources.Monitor
	// Configuration for the nova syncer.
	Conf v1alpha1.NovaDatasource
	// Nova API client to fetch the data.
	API NovaAPI
}

// Init the OpenStack nova syncer.
func (s *NovaSyncer) Init(ctx context.Context) error {
	if err := s.API.Init(ctx); err != nil {
		return err
	}
	tables := []*gorp.TableMap{}
	// Only add the tables that are configured in the yaml conf.
	switch s.Conf.Type {
	case v1alpha1.NovaDatasourceTypeServers:
		tables = append(tables, s.DB.AddTable(nova.Server{}))
	case v1alpha1.NovaDatasourceTypeDeletedServers:
		tables = append(tables, s.DB.AddTable(nova.DeletedServer{}))
	case v1alpha1.NovaDatasourceTypeHypervisors:
		tables = append(tables, s.DB.AddTable(nova.Hypervisor{}))
	case v1alpha1.NovaDatasourceTypeFlavors:
		tables = append(tables, s.DB.AddTable(nova.Flavor{}))
	case v1alpha1.NovaDatasourceTypeMigrations:
		tables = append(tables, s.DB.AddTable(nova.Migration{}))
	case v1alpha1.NovaDatasourceTypeAggregates:
		tables = append(tables, s.DB.AddTable(nova.Aggregate{}))
	}
	return s.DB.CreateTable(tables...)
}

// Sync the OpenStack nova objects and publish triggers.
func (s *NovaSyncer) Sync(ctx context.Context) (int64, error) {
	// Only sync the objects that are configured in the yaml conf.
	var err error
	var nResults int64
	switch s.Conf.Type {
	case v1alpha1.NovaDatasourceTypeServers:
		nResults, err = s.SyncAllServers(ctx)
	case v1alpha1.NovaDatasourceTypeDeletedServers:
		nResults, err = s.SyncDeletedServers(ctx)
	case v1alpha1.NovaDatasourceTypeHypervisors:
		nResults, err = s.SyncAllHypervisors(ctx)
	case v1alpha1.NovaDatasourceTypeFlavors:
		nResults, err = s.SyncAllFlavors(ctx)
	case v1alpha1.NovaDatasourceTypeMigrations:
		nResults, err = s.SyncAllMigrations(ctx)
	case v1alpha1.NovaDatasourceTypeAggregates:
		nResults, err = s.SyncAllAggregates(ctx)
	}
	return nResults, err
}

// Sync all the active OpenStack servers into the database. (Includes ERROR, SHUTOFF, etc. state)
func (s *NovaSyncer) SyncAllServers(ctx context.Context) (int64, error) {
	allServers, err := s.API.GetAllServers(ctx)
	if err != nil {
		return 0, err
	}
	err = db.ReplaceAll(s.DB, allServers...)
	if err != nil {
		return 0, err
	}
	label := nova.Server{}.TableName()
	if s.Mon.PipelineObjectsGauge != nil {
		gauge := s.Mon.PipelineObjectsGauge.WithLabelValues(label)
		gauge.Set(float64(len(allServers)))
	}
	if s.Mon.PipelineRequestProcessedCounter != nil {
		counter := s.Mon.PipelineRequestProcessedCounter.WithLabelValues(label)
		counter.Inc()
	}
	return int64(len(allServers)), nil
}

// Sync all the deleted OpenStack servers into the database.
// Only fetch servers that were deleted since the last sync run.
func (s *NovaSyncer) SyncDeletedServers(ctx context.Context) (int64, error) {
	// Default time frame is the last 6 hours
	since := time.Now().Add(-6 * time.Hour)

	// If there is a configured value, use that instead.
	if s.Conf.DeletedServersChangesSinceMinutes != nil {
		since = time.Now().Add(-time.Duration(*s.Conf.DeletedServersChangesSinceMinutes) * time.Minute)
	}

	deletedServers, err := s.API.GetDeletedServers(ctx, since)
	if err != nil {
		return 0, err
	}
	err = db.ReplaceAll(s.DB, deletedServers...)
	if err != nil {
		return 0, err
	}
	label := nova.DeletedServer{}.TableName()
	if s.Mon.PipelineObjectsGauge != nil {
		gauge := s.Mon.PipelineObjectsGauge.WithLabelValues(label)
		gauge.Set(float64(len(deletedServers)))
	}
	if s.Mon.PipelineRequestProcessedCounter != nil {
		counter := s.Mon.PipelineRequestProcessedCounter.WithLabelValues(label)
		counter.Inc()
	}
	return int64(len(deletedServers)), nil
}

// Sync the OpenStack hypervisors into the database.
func (s *NovaSyncer) SyncAllHypervisors(ctx context.Context) (int64, error) {
	allHypervisors, err := s.API.GetAllHypervisors(ctx)
	if err != nil {
		return 0, err
	}
	// Since the nova api doesn't support only returning changed
	// hypervisors, we can just replace all hypervisors in the database.
	err = db.ReplaceAll(s.DB, allHypervisors...)
	if err != nil {
		return 0, err
	}
	label := nova.Hypervisor{}.TableName()
	if s.Mon.PipelineObjectsGauge != nil {
		gauge := s.Mon.PipelineObjectsGauge.WithLabelValues(label)
		gauge.Set(float64(len(allHypervisors)))
	}
	if s.Mon.PipelineRequestProcessedCounter != nil {
		counter := s.Mon.PipelineRequestProcessedCounter.WithLabelValues(label)
		counter.Inc()
	}
	return int64(len(allHypervisors)), nil
}

// Sync the OpenStack flavors into the database.
func (s *NovaSyncer) SyncAllFlavors(ctx context.Context) (int64, error) {
	allFlavors, err := s.API.GetAllFlavors(ctx)
	if err != nil {
		return 0, err
	}
	err = db.ReplaceAll(s.DB, allFlavors...)
	if err != nil {
		return 0, err
	}
	label := nova.Flavor{}.TableName()
	if s.Mon.PipelineObjectsGauge != nil {
		gauge := s.Mon.PipelineObjectsGauge.WithLabelValues(label)
		gauge.Set(float64(len(allFlavors)))
	}
	if s.Mon.PipelineRequestProcessedCounter != nil {
		counter := s.Mon.PipelineRequestProcessedCounter.WithLabelValues(label)
		counter.Inc()
	}
	return int64(len(allFlavors)), nil
}

// Sync the OpenStack migrations into the database.
func (s *NovaSyncer) SyncAllMigrations(ctx context.Context) (int64, error) {
	allMigrations, err := s.API.GetAllMigrations(ctx)
	if err != nil {
		return 0, err
	}
	err = db.ReplaceAll(s.DB, allMigrations...)
	if err != nil {
		return 0, err
	}
	label := nova.Migration{}.TableName()
	if s.Mon.PipelineObjectsGauge != nil {
		gauge := s.Mon.PipelineObjectsGauge.WithLabelValues(label)
		gauge.Set(float64(len(allMigrations)))
	}
	if s.Mon.PipelineRequestProcessedCounter != nil {
		counter := s.Mon.PipelineRequestProcessedCounter.WithLabelValues(label)
		counter.Inc()
	}
	return int64(len(allMigrations)), nil
}

// Sync the OpenStack aggregates into the database.
func (s *NovaSyncer) SyncAllAggregates(ctx context.Context) (int64, error) {
	allAggregates, err := s.API.GetAllAggregates(ctx)
	if err != nil {
		return 0, err
	}
	err = db.ReplaceAll(s.DB, allAggregates...)
	if err != nil {
		return 0, err
	}
	label := nova.Aggregate{}.TableName()
	if s.Mon.PipelineObjectsGauge != nil {
		gauge := s.Mon.PipelineObjectsGauge.WithLabelValues(label)
		gauge.Set(float64(len(allAggregates)))
	}
	if s.Mon.PipelineRequestProcessedCounter != nil {
		counter := s.Mon.PipelineRequestProcessedCounter.WithLabelValues(label)
		counter.Inc()
	}
	return int64(len(allAggregates)), nil
}
