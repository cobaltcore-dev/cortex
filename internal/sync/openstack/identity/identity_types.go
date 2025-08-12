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

	// Not all fields are synced to the database
	// Fields that are omitted: description, links
}

// Table in which the openstack model is stored.
func (d Domain) TableName() string { return "openstack_domains" }

// Indexes for the domain table.
func (d Domain) Indexes() []db.Index { return nil }

// RawProject represents the raw project data as returned by the OpenStack identity API.
type RawProject struct {
	ID       string   `json:"id"`
	Name     string   `json:"name"`
	DomainID string   `json:"domain_id"`
	ParentID string   `json:"parent_id"`
	IsDomain bool     `json:"is_domain"`
	Enabled  bool     `json:"enabled"`
	Tags     []string `json:"tags"`
}

// Project as inserted into the database for efficient handling.
type Project struct {
	ID       string `json:"id" db:"id,primarykey"`
	Name     string `json:"name" db:"name"`
	DomainID string `json:"domain_id" db:"domain_id"`
	ParentID string `json:"parent_id" db:"parent_id"`
	IsDomain bool   `json:"is_domain" db:"is_domain"`
	Enabled  bool   `json:"enabled" db:"enabled"`
	Tags     string `json:"tags" db:"tags"` // Comma-separated tags

	// Not all fields are synced to the database.
	// Fields that are omitted: description, links
}

// Table in which the openstack model is stored.
func (p Project) TableName() string { return "openstack_projects" }

// Indexes for the project table.
func (p Project) Indexes() []db.Index { return nil }
