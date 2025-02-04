// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package vmware

import (
	"github.com/cobaltcore-dev/cortex/internal/db"
	"github.com/go-pg/pg/v10/orm"
)

type VROpsHostsystemContention struct {
	//lint:ignore U1000 Ignore unused field warning
	tableName        struct{} `pg:"feature_vrops_hostsystem_contention"`
	ComputeHost      string   `pg:"compute_host,notnull"`
	AvgCPUContention float64  `pg:"avg_cpu_contention,notnull"`
	MaxCPUContention float64  `pg:"max_cpu_contention,notnull"`
}

type VROpsHostsystemContentionExtractor struct {
	DB db.DB
}

func (e *VROpsHostsystemContentionExtractor) GetName() string {
	return "vrops_hostsystem_contention_extractor"
}

func (e *VROpsHostsystemContentionExtractor) Init(db db.DB, opts map[string]any) error {
	e.DB = db
	if err := e.DB.Get().Model((*VROpsHostsystemContention)(nil)).CreateTable(&orm.CreateTableOptions{
		IfNotExists: true,
	}); err != nil {
		return err
	}
	return nil
}

// Extract CPU contention of hostsystems.
// Depends on resolved vROps hostsystems (feature_vrops_resolved_hostsystem).
func (e *VROpsHostsystemContentionExtractor) Extract() error {
	// Delete the old data in the same transaction.
	tx, err := e.DB.Get().Begin()
	if err != nil {
		return err
	}
	defer tx.Close()
	if _, err := tx.Exec("DELETE FROM feature_vrops_hostsystem_contention"); err != nil {
		return tx.Rollback()
	}
	if _, err := tx.Exec(`
		INSERT INTO feature_vrops_hostsystem_contention (compute_host, avg_cpu_contention, max_cpu_contention)
		SELECT
			h.nova_compute_host AS compute_host,
			AVG(m.value) AS avg_cpu_contention,
			MAX(m.value) AS max_cpu_contention
		FROM vrops_host_metrics m
		JOIN feature_vrops_resolved_hostsystem h ON m.hostsystem = h.vrops_hostsystem
		WHERE m.name = 'vrops_hostsystem_cpu_contention_percentage'
		GROUP BY h.nova_compute_host;
    `); err != nil {
		return tx.Rollback()
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	return nil
}
