// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package cinder

import (
	"github.com/cobaltcore-dev/cortex/internal/conf"
	"github.com/cobaltcore-dev/cortex/internal/db"
)

// Type alias for the OpenStack Cinder configuration.
type CinderConf = conf.SyncOpenStackCinderConfig

type StoragePool struct {
	Name string `json:"name" db:"name,primarykey"`
}

// The table name for the storage pool model.
func (StoragePool) TableName() string { return "openstack_cinder_storage_pools" }

// Index for the openstack model.
func (StoragePool) Indexes() []db.Index { return nil }
