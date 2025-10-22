// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package manila

import (
	"context"

	"github.com/cobaltcore-dev/cortex/knowledge/api/datasources/openstack/manila"
	"github.com/cobaltcore-dev/cortex/knowledge/api/v1alpha1"
	"github.com/cobaltcore-dev/cortex/knowledge/internal/datasources"
	"github.com/cobaltcore-dev/cortex/lib/db"
	"github.com/go-gorp/gorp"
)

// Syncer for OpenStack manila.
type ManilaSyncer struct {
	// Database to store the manila objects in.
	DB db.DB
	// Monitor to track the syncer.
	Mon datasources.Monitor
	// Configuration for the manila syncer.
	Conf v1alpha1.ManilaDatasource
	// Manila API client to fetch the data.
	API ManilaAPI
}

// Init the OpenStack manila syncer.
func (s *ManilaSyncer) Init(ctx context.Context) error {
	if err := s.API.Init(ctx); err != nil {
		return err
	}
	tables := []*gorp.TableMap{}
	// Only add the tables that are configured in the yaml conf.
	switch s.Conf.Type {
	case v1alpha1.ManilaDatasourceTypeStoragePools:
		tables = append(tables, s.DB.AddTable(manila.StoragePool{}))
	}
	return s.DB.CreateTable(tables...)
}

// Sync the OpenStack manila objects and publish triggers.
func (s *ManilaSyncer) Sync(ctx context.Context) (int64, error) {
	// Only sync the objects that are configured in the yaml conf.
	var err error
	var nResults int64
	switch s.Conf.Type {
	case v1alpha1.ManilaDatasourceTypeStoragePools:
		nResults, err = s.SyncAllStoragePools(ctx)
	}
	return nResults, err
}

// Sync the OpenStack resource providers into the database.
func (s *ManilaSyncer) SyncAllStoragePools(ctx context.Context) (int64, error) {
	pools, err := s.API.GetAllStoragePools(ctx)
	if err != nil {
		return 0, err
	}
	if err := db.ReplaceAll(s.DB, pools...); err != nil {
		return 0, err
	}
	label := manila.StoragePool{}.TableName()
	if s.Mon.ObjectsGauge != nil {
		gauge := s.Mon.ObjectsGauge.WithLabelValues(label)
		gauge.Set(float64(len(pools)))
	}
	if s.Mon.RequestProcessedCounter != nil {
		counter := s.Mon.RequestProcessedCounter.WithLabelValues(label)
		counter.Inc()
	}
	return int64(len(pools)), nil
}
