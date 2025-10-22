// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package identity

import (
	"context"

	"github.com/cobaltcore-dev/cortex/knowledge/api/datasources/openstack/identity"
	"github.com/cobaltcore-dev/cortex/knowledge/api/v1alpha1"
	"github.com/cobaltcore-dev/cortex/knowledge/internal/datasources"
	"github.com/cobaltcore-dev/cortex/lib/db"
	"github.com/go-gorp/gorp"
)

type IdentitySyncer struct {
	// Database to store the identity objects in.
	DB db.DB
	// Monitor to track the syncer.
	Mon datasources.Monitor
	// Configuration for the identity syncer.
	Conf v1alpha1.IdentityDatasource
	// Identity API client to fetch the data.
	API IdentityAPI
}

func (s *IdentitySyncer) Init(ctx context.Context) error {
	if err := s.API.Init(ctx); err != nil {
		return err
	}
	var tables = []*gorp.TableMap{}
	// Only add the tables that are configured in the yaml conf.
	switch s.Conf.Type {
	case v1alpha1.IdentityDatasourceTypeDomains:
		tables = append(tables, s.DB.AddTable(identity.Domain{}))
	case v1alpha1.IdentityDatasourceTypeProjects:
		tables = append(tables, s.DB.AddTable(identity.Project{}))
	}
	return s.DB.CreateTable(tables...)
}

func (s *IdentitySyncer) Sync(ctx context.Context) (int64, error) {
	// Only sync the objects that are configured in the yaml conf.
	var err error
	var nResults int64
	switch s.Conf.Type {
	case v1alpha1.IdentityDatasourceTypeDomains:
		nResults, err = s.SyncDomains(ctx)
	case v1alpha1.IdentityDatasourceTypeProjects:
		nResults, err = s.SyncProjects(ctx)
	}
	return nResults, err
}

func (s *IdentitySyncer) SyncDomains(ctx context.Context) (int64, error) {
	domains, err := s.API.GetAllDomains(ctx)
	if err != nil {
		return 0, err
	}
	if err := db.ReplaceAll(s.DB, domains...); err != nil {
		return 0, err
	}
	label := identity.Domain{}.TableName()
	if s.Mon.ObjectsGauge != nil {
		gauge := s.Mon.ObjectsGauge.WithLabelValues(label)
		gauge.Set(float64(len(domains)))
	}
	if s.Mon.RequestProcessedCounter != nil {
		counter := s.Mon.RequestProcessedCounter.WithLabelValues(label)
		counter.Inc()
	}
	return int64(len(domains)), nil
}

func (s *IdentitySyncer) SyncProjects(ctx context.Context) (int64, error) {
	projects, err := s.API.GetAllProjects(ctx)
	if err != nil {
		return 0, err
	}
	if err := db.ReplaceAll(s.DB, projects...); err != nil {
		return 0, err
	}
	label := identity.Project{}.TableName()
	if s.Mon.ObjectsGauge != nil {
		gauge := s.Mon.ObjectsGauge.WithLabelValues(label)
		gauge.Set(float64(len(projects)))
	}
	if s.Mon.RequestProcessedCounter != nil {
		counter := s.Mon.RequestProcessedCounter.WithLabelValues(label)
		counter.Inc()
	}
	return int64(len(projects)), nil
}
