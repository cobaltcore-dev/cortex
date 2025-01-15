// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package openstack

import (
	"github.com/cobaltcore-dev/cortex/internal/conf"
	"github.com/cobaltcore-dev/cortex/internal/db"
	"github.com/cobaltcore-dev/cortex/internal/logging"

	"github.com/go-pg/pg/v10/orm"
)

// Create the necessary database tables if they do not exist.
func Init() {
	models := []any{
		(*OpenStackServer)(nil),
		(*OpenStackHypervisor)(nil),
	}
	for _, model := range models {
		if err := db.Get().Model(model).CreateTable(&orm.CreateTableOptions{
			IfNotExists: true,
		}); err != nil {
			panic(err)
		}
	}
}

// Sync OpenStack data with the database.
func Sync() {
	logging.Log.Info("syncing OpenStack data with", "authUrl", conf.Get().OSAuthURL)
	auth, err := getKeystoneAuth()
	if err != nil {
		logging.Log.Error("failed to get keystone auth", "error", err)
		return
	}
	serverlist, err := getServers(*auth, nil)
	if err != nil {
		logging.Log.Error("failed to get servers", "error", err)
		return
	}
	hypervisorlist, err := getHypervisors(*auth, nil)
	if err != nil {
		logging.Log.Error("failed to get hypervisors", "error", err)
		return
	}
	// Insert in small batches to avoid OOM issues.
	batchSize := 100
	for i := 0; i < len(serverlist.Servers); i += batchSize {
		servers := serverlist.Servers[i:min(i+batchSize, len(serverlist.Servers))]
		if _, err = db.Get().Model(&servers).
			OnConflict("(id) DO UPDATE").
			Insert(); err != nil {
			logging.Log.Error("failed to insert servers", "error", err)
			return
		}
	}
	for i := 0; i < len(hypervisorlist.Hypervisors); i += batchSize {
		hypervisors := hypervisorlist.Hypervisors[i:min(i+batchSize, len(hypervisorlist.Hypervisors))]
		if _, err = db.Get().Model(&hypervisors).
			OnConflict("(id) DO UPDATE").
			Insert(); err != nil {
			logging.Log.Error("failed to insert hypervisors", "error", err)
			return
		}
	}
	logging.Log.Info("synced OpenStack data", "servers", len(serverlist.Servers), "hypervisors", len(hypervisorlist.Hypervisors))
}
