// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package identity

import (
	"github.com/cobaltcore-dev/cortex/internal/conf"
	"github.com/cobaltcore-dev/cortex/internal/db"
)

type IdentityConf = conf.SyncOpenStackIdentityConfig

type Domain struct {
	ID      string `json:"id" db:"id,primarykey"`
	Name    string `json:"name" db:"name"`
	Enabled bool   `json:"enabled" db:"enabled"`
}

// Table in which the openstack model is stored.
func (d Domain) TableName() string { return "openstack_domains" }

// Indexes for the domain table.
func (d Domain) Indexes() []db.Index { return nil }

type Project struct {
	ID       string `json:"id" db:"id,primarykey"`
	Name     string `json:"name" db:"name"`
	DomainID string `json:"domain_id" db:"domain_id"`
}

// Table in which the openstack model is stored.
func (p Project) TableName() string { return "openstack_projects" }

// Indexes for the project table.
func (p Project) Indexes() []db.Index { return nil }
