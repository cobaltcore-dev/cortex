// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package vmware

import (
	"github.com/cobaltcore-dev/cortex/internal/features/plugins"
)

// Feature that calculates the noisiness of projects and on which
// compute hosts they are currently running.
type VROpsProjectNoisiness struct {
	Project         string  `db:"project"`
	ComputeHost     string  `db:"compute_host"`
	AvgCPUOfProject float64 `db:"avg_cpu_of_project"`
}

// Table under which the feature is stored.
func (VROpsProjectNoisiness) TableName() string {
	return "feature_vrops_project_noisiness"
}

// Extractor that extracts the noisiness of projects and on which compute
// hosts they are currently running and stores it as a feature into the database.
type VROpsProjectNoisinessExtractor struct {
	// Common base for all extractors that provides standard functionality.
	plugins.BaseExtractor[
		struct{},              // No options passed through yaml config
		VROpsProjectNoisiness, // Feature model
	]
}

// Name of this feature extractor that is used in the yaml config, for logging etc.
func (e *VROpsProjectNoisinessExtractor) GetName() string {
	return "vrops_project_noisiness_extractor"
}

// Extract the noisiness for each project in OpenStack with the following steps:
// 1. Get the average cpu usage of each project through the vROps metrics.
// 2. Find on which hosts the projects are currently running through the
// OpenStack servers and hypervisors.
// 3. Store the avg cpu usage together with the current hosts in the database.
// This feature can then be used to draw new VMs away from VMs of the same
// project in case this project is known to cause high cpu usage.
func (e *VROpsProjectNoisinessExtractor) Extract() ([]plugins.Feature, error) {
	var features []VROpsProjectNoisiness
	if _, err := e.DB.Select(&features, `
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
        SELECT
            tenant_id AS project,
            service_host AS compute_host,
            avg_cpu_of_project
        FROM host_cpu_usage;
    `); err != nil {
		return nil, err
	}
	return e.Extracted(features)
}
