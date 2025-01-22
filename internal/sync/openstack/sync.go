// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package openstack

import (
	"errors"

	"github.com/cobaltcore-dev/cortex/internal/conf"
	"github.com/cobaltcore-dev/cortex/internal/db"
	"github.com/cobaltcore-dev/cortex/internal/logging"
	"github.com/cobaltcore-dev/cortex/internal/sync"
	"github.com/prometheus/client_golang/prometheus"

	"github.com/go-pg/pg/v10"
	"github.com/go-pg/pg/v10/orm"
)

type syncer struct {
	Config        conf.SyncOpenStackConfig
	ServerAPI     ServerAPI
	HypervisorAPI HypervisorAPI
	KeystoneAPI   KeystoneAPI
	DB            db.DB
	monitor       sync.Monitor
}

// Create a new OpenStack syncer with the given configuration and database.
func NewSyncer(config conf.Config, db db.DB, monitor sync.Monitor) sync.Datasource {
	return &syncer{
		Config:        config.GetSyncConfig().OpenStack,
		ServerAPI:     NewServerAPI(monitor),
		HypervisorAPI: NewHypervisorAPI(monitor),
		KeystoneAPI:   NewKeystoneAPI(monitor),
		DB:            db,
		monitor:       monitor,
	}
}

// Create the necessary database tables if they do not exist.
func (s *syncer) Init() {
	models := []any{}
	if s.Config.ServersEnabled != nil && *s.Config.ServersEnabled {
		models = append(models, (*OpenStackServer)(nil))
	}
	if s.Config.HypervisorsEnabled != nil && *s.Config.HypervisorsEnabled {
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

func (s *syncer) syncServers(auth *openStackKeystoneAuth, tx *pg.Tx) error {
	if s.Config.ServersEnabled == nil {
		return errors.New("servers not enabled")
	}
	if !*s.Config.ServersEnabled {
		return errors.New("servers not enabled")
	}
	if _, err := tx.Model((*OpenStackServer)(nil)).Where("TRUE").Delete(); err != nil {
		logging.Log.Error("failed to delete old servers", "error", err)
		return err
	}
	serverlist, err := s.ServerAPI.Get(*auth, nil)
	if err != nil {
		logging.Log.Error("failed to get servers", "error", err)
		return err
	}
	const batchSize = 100
	for i := 0; i < len(serverlist.Servers); i += batchSize {
		servers := serverlist.Servers[i:min(i+batchSize, len(serverlist.Servers))]
		if _, err = tx.Model(&servers).
			OnConflict("(id) DO UPDATE").
			Insert(); err != nil {
			logging.Log.Error("failed to insert servers", "error", err)
			return err
		}
	}
	if s.monitor.PipelineObjectsGauge != nil {
		s.monitor.PipelineObjectsGauge.
			WithLabelValues("openstack_nova_servers").
			Set(float64(len(serverlist.Servers)))
	}
	logging.Log.Info("synced OpenStack", "servers", len(serverlist.Servers))
	return nil
}

func (s *syncer) syncHypervisors(auth *openStackKeystoneAuth, tx *pg.Tx) error {
	if s.Config.HypervisorsEnabled == nil {
		return errors.New("hypervisors not enabled")
	}
	if !*s.Config.HypervisorsEnabled {
		return errors.New("hypervisors not enabled")
	}
	if _, err := tx.Model((*OpenStackHypervisor)(nil)).Where("TRUE").Delete(); err != nil {
		logging.Log.Error("failed to delete old hypervisors", "error", err)
		return err
	}
	hypervisorlist, err := s.HypervisorAPI.Get(*auth, nil)
	if err != nil {
		logging.Log.Error("failed to get hypervisors", "error", err)
		return err
	}
	const batchSize = 100
	for i := 0; i < len(hypervisorlist.Hypervisors); i += batchSize {
		hypervisors := hypervisorlist.Hypervisors[i:min(i+batchSize, len(hypervisorlist.Hypervisors))]
		if _, err = tx.Model(&hypervisors).
			OnConflict("(id) DO UPDATE").
			Insert(); err != nil {
			logging.Log.Error("failed to insert hypervisors", "error", err)
			return err
		}
	}
	if s.monitor.PipelineObjectsGauge != nil {
		s.monitor.PipelineObjectsGauge.
			WithLabelValues("openstack_nova_hypervisors").
			Set(float64(len(hypervisorlist.Hypervisors)))
	}
	logging.Log.Info("synced OpenStack", "hypervisors", len(hypervisorlist.Hypervisors))
	return nil
}

// Sync OpenStack data with the database.
func (s *syncer) Sync() {
	if s.monitor.PipelineRunTimer != nil {
		hist := s.monitor.PipelineRunTimer.WithLabelValues("openstack")
		timer := prometheus.NewTimer(hist)
		defer timer.ObserveDuration()
	}

	logging.Log.Info("syncing OpenStack data")

	auth, err := s.KeystoneAPI.Authenticate()
	if err != nil {
		logging.Log.Error("failed to get keystone auth", "error", err)
		return
	}

	var syncPartials = []func(*openStackKeystoneAuth, *pg.Tx) error{
		s.syncServers,
		s.syncHypervisors,
	}
	for _, syncPartial := range syncPartials {
		tx, err := s.DB.Get().Begin()
		if err != nil {
			logging.Log.Error("failed to begin transaction", "error", err)
			return
		}
		if err := syncPartial(auth, tx); err != nil {
			if err := tx.Rollback(); err != nil {
				// Don't log if the transaction has been committed
				logging.Log.Error("failed to rollback transaction", "error", err)
			}
			return
		}
		if err := tx.Commit(); err != nil {
			logging.Log.Error("failed to commit transaction", "error", err)
			return
		}
	}
}
