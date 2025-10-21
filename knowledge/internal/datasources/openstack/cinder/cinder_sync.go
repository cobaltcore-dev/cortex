// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package cinder

import (
	"context"

	"github.com/cobaltcore-dev/cortex/knowledge/api/datasources/openstack/cinder"
	"github.com/cobaltcore-dev/cortex/knowledge/api/v1alpha1"
	"github.com/cobaltcore-dev/cortex/knowledge/internal/datasources"
	"github.com/cobaltcore-dev/cortex/lib/db"
	"github.com/go-gorp/gorp"
)

type CinderSyncer struct {
	// Database to store the manila objects in.
	DB db.DB
	// Monitor to track the syncer.
	Mon datasources.Monitor
	// Configuration for the cinder syncer.
	Conf v1alpha1.CinderDatasource
	// Cinder API client to fetch the data.
	API CinderAPI
}

// Init the OpenStack cinder syncer.
func (s *CinderSyncer) Init(ctx context.Context) error {
	s.API.Init(ctx)
	tables := []*gorp.TableMap{}
	// Only add the tables that are configured in the yaml conf.
	switch s.Conf.Type {
	case v1alpha1.CinderDatasourceTypeStoragePools:
		tables = append(tables, s.DB.AddTable(cinder.StoragePool{}))
	}
	return s.DB.CreateTable(tables...)
}

// Sync the OpenStack cinder objects and publish triggers.
func (s *CinderSyncer) Sync(ctx context.Context) (int64, error) {
	// Only sync the objects that are configured in the yaml conf.
	var err error
	var nResults int64
	switch s.Conf.Type {
	case v1alpha1.CinderDatasourceTypeStoragePools:
		nResults, err = s.SyncAllStoragePools(ctx)
	}
	return nResults, err
}

// Sync the OpenStack resource providers into the database.
func (s *CinderSyncer) SyncAllStoragePools(ctx context.Context) (int64, error) {
	pools, err := s.API.GetAllStoragePools(ctx)
	if err != nil {
		return 0, err
	}
	if err := db.ReplaceAll(s.DB, pools...); err != nil {
		return 0, err
	}
	label := cinder.StoragePool{}.TableName()
	if s.Mon.PipelineObjectsGauge != nil {
		gauge := s.Mon.PipelineObjectsGauge.WithLabelValues(label)
		gauge.Set(float64(len(pools)))
	}
	if s.Mon.PipelineRequestProcessedCounter != nil {
		counter := s.Mon.PipelineRequestProcessedCounter.WithLabelValues(label)
		counter.Inc()
	}
	return int64(len(pools)), nil
}
