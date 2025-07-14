// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package identity

import (
	"context"
	"slices"

	"github.com/cobaltcore-dev/cortex/internal/db"
	"github.com/cobaltcore-dev/cortex/internal/mqtt"
	"github.com/cobaltcore-dev/cortex/internal/sync"
	"github.com/go-gorp/gorp"
)

type IdentitySyncer struct {
	DB         db.DB
	Mon        sync.Monitor
	API        IdentityAPI
	MqttClient mqtt.Client
	Conf       IdentityConf
}

func (s *IdentitySyncer) Init(ctx context.Context) {
	s.API.Init(ctx)
	var tables = []*gorp.TableMap{}

	if slices.Contains(s.Conf.Types, "domains") {
		tables = append(tables, s.DB.AddTable(Domain{}))
	}
	if slices.Contains(s.Conf.Types, "projects") {
		tables = append(tables, s.DB.AddTable(Project{}))
	}
	if err := s.DB.CreateTable(tables...); err != nil {
		panic(err)
	}
}

func (s *IdentitySyncer) Sync(ctx context.Context) error {
	if slices.Contains(s.Conf.Types, "domains") {
		domains, err := s.SyncDomains(ctx)
		if err != nil {
			return err
		}
		if len(domains) > 0 {
			go s.MqttClient.Publish(TriggerIdentityDomainsSynced, "")
		}
	}
	if slices.Contains(s.Conf.Types, "projects") {
		projects, err := s.SyncProjects(ctx)
		if err != nil {
			return err
		}
		if len(projects) > 0 {
			go s.MqttClient.Publish(TriggerIdentityProjectsSynced, "")
		}
	}
	return nil
}

func (s *IdentitySyncer) SyncDomains(ctx context.Context) ([]Domain, error) {
	domains, err := s.API.GetAllDomains(ctx)
	if err != nil {
		return nil, err
	}
	if err := db.ReplaceAll(s.DB, domains...); err != nil {
		return nil, err
	}
	return domains, nil
}

func (s *IdentitySyncer) SyncProjects(ctx context.Context) ([]Project, error) {
	projects, err := s.API.GetAllProjects(ctx)
	if err != nil {
		return nil, err
	}
	if err := db.ReplaceAll(s.DB, projects...); err != nil {
		return nil, err
	}
	return projects, nil
}
