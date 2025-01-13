// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package features

import (
	"github.com/cobaltcore-dev/cortex/internal/db"
	"github.com/cobaltcore-dev/cortex/internal/logging"
	"github.com/go-pg/pg/v10/orm"
)

type ProjectNoisiness struct {
	//lint:ignore U1000 Ignore unused field warning
	tableName       struct{} `pg:"feature_project_noisiness"`
	Project         string   `pg:"project,notnull"`
	Host            string   `pg:"host,notnull"`
	AvgCPUOfProject float64  `pg:"avg_cpu_of_project,notnull"`
}

func projectNoisinessSchema() error {
	if err := db.DB.Model((*ProjectNoisiness)(nil)).CreateTable(&orm.CreateTableOptions{
		IfNotExists: true,
	}); err != nil {
		return err
	}
	return nil
}

func projectNoisinessExtractor() error {
	logging.Log.Info("extracting noisy projects")
	tx, err := db.DB.Begin()
	if err != nil {
		return err
	}
	defer tx.Close()
	if _, err := tx.Exec("DELETE FROM feature_project_noisiness"); err != nil {
		tx.Rollback()
		return err
	}
	if _, err := tx.Exec(`
        WITH projects_avg_cpu AS (
            SELECT
                m.project AS tenant_id,
                AVG(m.value) AS avg_cpu
            FROM metrics m
            WHERE m.name = 'vrops_virtualmachine_cpu_demand_ratio'
            GROUP BY m.project
            ORDER BY avg_cpu DESC
        ),
        host_cpu_usage AS (
            SELECT
                s.tenant_id,
                h.service_host,
                AVG(p.avg_cpu) AS avg_cpu_of_project
            FROM openstack_servers s
            JOIN metrics m ON s.id = m.instance_uuid
            JOIN projects_avg_cpu p ON s.tenant_id = p.tenant_id
            JOIN openstack_hypervisors h ON s.os_ext_srv_attr_hypervisor_hostname = h.hostname
            GROUP BY s.tenant_id, h.service_host
			ORDER BY avg_cpu_of_project DESC
        )
		INSERT INTO feature_project_noisiness
        SELECT
            tenant_id,
            service_host,
            avg_cpu_of_project
        FROM host_cpu_usage;
    `); err != nil {
		tx.Rollback()
		return err
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	return nil
}
