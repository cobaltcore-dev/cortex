// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package limes

import (
	"context"

	"github.com/cobaltcore-dev/cortex/knowledge/api/datasources/openstack/identity"
	"github.com/cobaltcore-dev/cortex/knowledge/api/datasources/openstack/limes"
	"github.com/cobaltcore-dev/cortex/knowledge/api/v1alpha1"
	"github.com/cobaltcore-dev/cortex/knowledge/internal/datasources"
	"github.com/cobaltcore-dev/cortex/lib/db"
	"github.com/go-gorp/gorp"
)

// Syncer for limes.
type LimesSyncer struct {
	// Database to store the limes objects in.
	DB db.DB
	// Monitor to track the syncer.
	Mon datasources.Monitor
	// Configuration for the limes syncer.
	Conf v1alpha1.LimesDatasource
	// Limes API client to fetch the data.
	API LimesAPI
}

func (s *LimesSyncer) Init(ctx context.Context) error {
	if err := s.API.Init(ctx); err != nil {
		return err
	}
	var tables = []*gorp.TableMap{}
	// Only add the tables that are configured in the yaml conf.
	switch s.Conf.Type {
	case v1alpha1.LimesDatasourceTypeProjectCommitments:
		tables = append(tables, s.DB.AddTable(limes.Commitment{}))
	}
	return s.DB.CreateTable(tables...)
}

// Sync the limes objects.
func (s *LimesSyncer) Sync(ctx context.Context) (int64, error) {
	// Only sync the objects that are configured in the yaml conf.
	var err error
	var nResults int64
	switch s.Conf.Type {
	case v1alpha1.LimesDatasourceTypeProjectCommitments:
		nResults, err = s.SyncCommitments(ctx)
	}
	return nResults, err
}

// Sync commitments from the limes API and store them in the database.
func (s *LimesSyncer) SyncCommitments(ctx context.Context) (int64, error) {
	var projects []identity.Project
	_, err := s.DB.Select(&projects, "SELECT * FROM "+identity.Project{}.TableName())
	if err != nil {
		return 0, v1alpha1.ErrWaitingForDependencyDatasource
	}
	if len(projects) == 0 {
		return 0, v1alpha1.ErrWaitingForDependencyDatasource
	}
	commitments, err := s.API.GetAllCommitments(ctx, projects)
	if err != nil {
		return 0, err
	}
	if err := db.ReplaceAll(s.DB, commitments...); err != nil {
		return 0, err
	}
	label := limes.Commitment{}.TableName()
	if s.Mon.ObjectsGauge != nil {
		gauge := s.Mon.ObjectsGauge.WithLabelValues(label)
		gauge.Set(float64(len(commitments)))
	}
	if s.Mon.RequestProcessedCounter != nil {
		counter := s.Mon.RequestProcessedCounter.WithLabelValues(label)
		counter.Inc()
	}
	return int64(len(commitments)), nil
}
