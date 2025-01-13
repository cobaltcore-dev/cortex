// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package openstack

import (
	"github.com/cobaltcore-dev/cortex/internal/conf"
	"github.com/cobaltcore-dev/cortex/internal/db"
	"github.com/cobaltcore-dev/cortex/internal/logging"

	"github.com/go-pg/pg/v10/orm"
)

func Init() {
	models := []any{
		(*OpenStackServer)(nil),
		(*OpenStackHypervisor)(nil),
	}
	for _, model := range models {
		if err := db.DB.Model(model).CreateTable(&orm.CreateTableOptions{
			IfNotExists: true,
		}); err != nil {
			panic(err)
		}
	}
}

func Sync() {
	logging.Log.Info("syncing OpenStack data with", "authUrl", conf.Get().OSAuthUrl)
	auth, err := getKeystoneAuth()
	if err != nil {
		logging.Log.Error("failed to get keystone auth", "error", err)
		return
	}
	serverlist, err := getServers(auth)
	if err != nil {
		logging.Log.Error("failed to get servers", "error", err)
		return
	}
	hypervisorlist, err := getHypervisors(auth)
	if err != nil {
		logging.Log.Error("failed to get hypervisors", "error", err)
		return
	}
	db.DB.Model(&serverlist.Servers).
		OnConflict("(id) DO UPDATE").
		Insert()
	db.DB.Model(&hypervisorlist.Hypervisors).
		OnConflict("(id) DO UPDATE").
		Insert()
	logging.Log.Info("synced OpenStack data", "servers", len(serverlist.Servers), "hypervisors", len(hypervisorlist.Hypervisors))
}
