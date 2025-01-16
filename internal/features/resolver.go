// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package features

import (
	"github.com/cobaltcore-dev/cortex/internal/db"
	"github.com/cobaltcore-dev/cortex/internal/logging"
	"github.com/go-pg/pg/v10/orm"
)

type ResolvedVROpsHostsystem struct {
	//lint:ignore U1000 Ignore unused field warning
	tableName       struct{} `pg:"feature_resolved_vrops_hostsystem"`
	VROpsHostsystem string   `pg:"vrops_hostsystem,notnull"`
	NovaComputeHost string   `pg:"nova_compute_host,notnull"`
}

type vropsHostsystemResolver struct {
	DB db.DB
}

func NewVROpsHostsystemResolver(db db.DB) FeatureExtractor {
	return &vropsHostsystemResolver{DB: db}
}

// Create the feature schema.
func (e *vropsHostsystemResolver) Init() error {
	if err := e.DB.Get().Model((*ResolvedVROpsHostsystem)(nil)).CreateTable(&orm.CreateTableOptions{
		IfNotExists: true,
	}); err != nil {
		return err
	}
	return nil
}

// Resolve vROps hostsystems to Nova compute hosts.
func (e *vropsHostsystemResolver) Extract() error {
	logging.Log.Info("resolving vROps hostsystems")
	// Delete the old data in the same transaction.
	tx, err := e.DB.Get().Begin()
	if err != nil {
		return err
	}
	defer tx.Close()
	if _, err := tx.Exec("DELETE FROM feature_resolved_vrops_hostsystem"); err != nil {
		return tx.Rollback()
	}
	if _, err := tx.Exec(`
		INSERT INTO feature_resolved_vrops_hostsystem (vrops_hostsystem, nova_compute_host)
		SELECT
			m.hostsystem AS hostsystem,
			h.service_host AS service_host
		FROM vrops_vm_metrics m
		JOIN openstack_servers s ON m.instance_uuid = s.id
		JOIN openstack_hypervisors h ON s.os_ext_srv_attr_hypervisor_hostname = h.hostname
		GROUP BY m.hostsystem, h.service_host;
    `); err != nil {
		return tx.Rollback()
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	return nil
}
