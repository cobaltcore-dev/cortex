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
	tableName    struct{} `pg:"feature_project_noisiness"`
	Project      string   `pg:"project,notnull"`
	Host         string   `pg:"host,notnull"`
	AvgCPUOnHost float64  `pg:"avg_cpu_on_host,notnull"`
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
        WITH cpu_usage AS (
            SELECT
                m.project AS tenant_id,
                m.instance_uuid AS instance_uuid,
                AVG(m.value) AS avg_cpu
            FROM metrics m
            WHERE m.name = 'vrops_virtualmachine_cpu_demand_ratio'
            GROUP BY m.project, m.instance_uuid
            ORDER BY avg_cpu DESC
        ),
        host_cpu_usage AS (
            SELECT
                s.tenant_id,
                h.service_host,
                AVG(cpu_usage.avg_cpu) AS avg_cpu_on_host
            FROM openstack_servers s
            JOIN metrics m ON s.id = m.instance_uuid
            JOIN cpu_usage ON m.instance_uuid = cpu_usage.instance_uuid
            JOIN openstack_hypervisors h ON s.os_ext_srv_attr_hypervisor_hostname = h.hostname
            WHERE s.tenant_id IN (SELECT tenant_id FROM cpu_usage)
            AND m.name = 'vrops_virtualmachine_cpu_demand_ratio'
            GROUP BY s.tenant_id, h.service_host
        )
		INSERT INTO feature_project_noisiness (project, host, avg_cpu_on_host)
        SELECT
            tenant_id,
            service_host,
            avg_cpu_on_host
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
