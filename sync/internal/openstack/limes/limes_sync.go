// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package limes

import (
	"context"
	"slices"

	"github.com/cobaltcore-dev/cortex/lib/db"
	"github.com/cobaltcore-dev/cortex/lib/mqtt"
	"github.com/cobaltcore-dev/cortex/sync/api/objects/openstack/identity"
	"github.com/cobaltcore-dev/cortex/sync/api/objects/openstack/limes"
	sync "github.com/cobaltcore-dev/cortex/sync/internal"
	"github.com/cobaltcore-dev/cortex/sync/internal/conf"
	"github.com/go-gorp/gorp"
)

// Syncer for limes.
type LimesSyncer struct {
	// Database to store the limes objects in.
	DB db.DB
	// Monitor to track the syncer.
	Mon sync.Monitor
	// Configuration for the limes syncer.
	Conf conf.SyncOpenStackLimesConfig
	// Limes API client to fetch the data.
	API LimesAPI
	// MQTT client to publish mqtt data.
	MqttClient mqtt.Client
}

func (s *LimesSyncer) Init(ctx context.Context) {
	s.API.Init(ctx)
	var tables = []*gorp.TableMap{}
	// Only add the tables that are configured in the yaml conf.
	if slices.Contains(s.Conf.Types, "commitments") {
		tables = append(tables, s.DB.AddTable(limes.Commitment{}))
	}
	if err := s.DB.CreateTable(tables...); err != nil {
		panic(err)
	}
}

// Sync the limes objects.
func (s *LimesSyncer) Sync(ctx context.Context) error {
	// Only sync the objects that are configured in the yaml conf.
	if slices.Contains(s.Conf.Types, "commitments") {
		_, err := s.SyncCommitments(ctx)
		if err != nil {
			return err
		}
		go s.MqttClient.Publish(limes.TriggerLimesCommitmentsSynced, "")
	}
	return nil
}

// Sync commitments from the limes API and store them in the database.
func (s *LimesSyncer) SyncCommitments(ctx context.Context) ([]limes.Commitment, error) {
	label := limes.Commitment{}.TableName()
	var projects []identity.Project
	_, err := s.DB.Select(&projects, "SELECT * FROM "+identity.Project{}.TableName())
	if err != nil {
		return nil, err
	}
	commitments, err := s.API.GetAllCommitments(ctx, projects)
	if err != nil {
		return nil, err
	}
	if err := db.ReplaceAll(s.DB, commitments...); err != nil {
		return nil, err
	}
	if s.Mon.PipelineObjectsGauge != nil {
		gauge := s.Mon.PipelineObjectsGauge.WithLabelValues(label)
		gauge.Set(float64(len(commitments)))
	}
	if s.Mon.PipelineRequestProcessedCounter != nil {
		counter := s.Mon.PipelineRequestProcessedCounter.WithLabelValues(label)
		counter.Inc()
	}
	return commitments, nil
}
