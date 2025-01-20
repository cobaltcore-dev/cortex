// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package openstack

import (
	"github.com/cobaltcore-dev/cortex/internal/conf"
	"github.com/cobaltcore-dev/cortex/internal/db"
	"github.com/cobaltcore-dev/cortex/internal/logging"
	"github.com/cobaltcore-dev/cortex/internal/sync"

	"github.com/go-pg/pg/v10/orm"
)

type syncer struct {
	Config        conf.SyncOpenStackConfig
	ServerAPI     ServerAPI
	HypervisorAPI HypervisorAPI
	KeystoneAPI   KeystoneAPI
	DB            db.DB
}

func NewSyncer(db db.DB) sync.Datasource {
	return &syncer{
		Config:        conf.NewConfig().GetSyncConfig().OpenStack,
		ServerAPI:     NewServerAPI(),
		HypervisorAPI: NewHypervisorAPI(),
		KeystoneAPI:   NewKeystoneAPI(),
		DB:            db,
	}
}

// Create the necessary database tables if they do not exist.
func (s *syncer) Init() {
	models := []any{}
	if s.Config.ServersEnabled {
		models = append(models, (*OpenStackServer)(nil))
	}
	if s.Config.HypervisorsEnabled {
		models = append(models, (*OpenStackHypervisor)(nil))
	}
	for _, model := range models {
		if err := s.DB.Get().Model(model).CreateTable(&orm.CreateTableOptions{
			IfNotExists: true,
		}); err != nil {
			panic(err)
		}
	}
}

// Sync OpenStack data with the database.
func (s *syncer) Sync() {
	logging.Log.Info("syncing OpenStack data")

	// Insert in small batches to avoid OOM issues.
	const batchSize = 100

	auth, err := s.KeystoneAPI.Authenticate()
	if err != nil {
		logging.Log.Error("failed to get keystone auth", "error", err)
		return
	}

	if s.Config.ServersEnabled {
		serverlist, err := s.ServerAPI.Get(*auth, nil)
		if err != nil {
			logging.Log.Error("failed to get servers", "error", err)
			return
		}
		for i := 0; i < len(serverlist.Servers); i += batchSize {
			servers := serverlist.Servers[i:min(i+batchSize, len(serverlist.Servers))]
			if _, err = s.DB.Get().Model(&servers).
				OnConflict("(id) DO UPDATE").
				Insert(); err != nil {
				logging.Log.Error("failed to insert servers", "error", err)
				return
			}
		}
		logging.Log.Info("synced OpenStack", "servers", len(serverlist.Servers))
	}

	if s.Config.HypervisorsEnabled {
		hypervisorlist, err := s.HypervisorAPI.Get(*auth, nil)
		if err != nil {
			logging.Log.Error("failed to get hypervisors", "error", err)
			return
		}
		for i := 0; i < len(hypervisorlist.Hypervisors); i += batchSize {
			hypervisors := hypervisorlist.Hypervisors[i:min(i+batchSize, len(hypervisorlist.Hypervisors))]
			if _, err = s.DB.Get().Model(&hypervisors).
				OnConflict("(id) DO UPDATE").
				Insert(); err != nil {
				logging.Log.Error("failed to insert hypervisors", "error", err)
				return
			}
		}
		logging.Log.Info("synced OpenStack", "hypervisors", len(hypervisorlist.Hypervisors))
	}
}
