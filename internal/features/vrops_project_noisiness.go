// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package features

import (
	"github.com/cobaltcore-dev/cortex/internal/db"
	"github.com/cobaltcore-dev/cortex/internal/logging"
	"github.com/go-pg/pg/v10/orm"
)

type VROpsProjectNoisiness struct {
	//lint:ignore U1000 Ignore unused field warning
	tableName       struct{} `pg:"feature_vrops_project_noisiness"`
	Project         string   `pg:"project,notnull"`
	ComputeHost     string   `pg:"compute_host,notnull"`
	AvgCPUOfProject float64  `pg:"avg_cpu_of_project,notnull"`
}

type vROpsProjectNoisinessExtractor struct {
	DB db.DB
}

func NewVROpsProjectNoisinessExtractor(db db.DB) FeatureExtractor {
	return &vROpsProjectNoisinessExtractor{DB: db}
}

// Create the schema for the project noisiness feature.
func (e *vROpsProjectNoisinessExtractor) Init() error {
	if err := e.DB.Get().Model((*VROpsProjectNoisiness)(nil)).CreateTable(&orm.CreateTableOptions{
		IfNotExists: true,
	}); err != nil {
		return err
	}
	return nil
}

// Extract the noisiness for each project in OpenStack with the following steps:
// 1. Get the average cpu usage of each project through the vROps metrics.
// 2. Find on which hosts the projects are currently running through the
// OpenStack servers and hypervisors.
// 3. Store the avg cpu usage together with the current hosts in the database.
// This feature can then be used to draw new VMs away from VMs of the same
// project in case this project is known to cause high cpu usage.
func (e *vROpsProjectNoisinessExtractor) Extract() error {
	logging.Log.Info("extracting noisy projects")
	// Delete the old data in the same transaction.
	tx, err := e.DB.Get().Begin()
	if err != nil {
		return err
	}
	defer tx.Close()
	if _, err := tx.Exec("DELETE FROM feature_vrops_project_noisiness"); err != nil {
		return tx.Rollback()
	}
	// Extract the new data.
	if _, err := tx.Exec(`
        WITH projects_avg_cpu AS (
            SELECT
                m.project AS tenant_id,
                AVG(m.value) AS avg_cpu
            FROM vrops_vm_metrics m
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
            JOIN vrops_vm_metrics m ON s.id = m.instance_uuid
            JOIN projects_avg_cpu p ON s.tenant_id = p.tenant_id
            JOIN openstack_hypervisors h ON s.os_ext_srv_attr_hypervisor_hostname = h.hostname
            GROUP BY s.tenant_id, h.service_host
			ORDER BY avg_cpu_of_project DESC
        )
		INSERT INTO feature_vrops_project_noisiness
        SELECT
            tenant_id,
            service_host,
            avg_cpu_of_project
        FROM host_cpu_usage;
    `); err != nil {
		return tx.Rollback()
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	return nil
}
