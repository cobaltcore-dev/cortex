// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package shared

import (
	_ "embed"

	"github.com/cobaltcore-dev/cortex/internal/db"
	"github.com/cobaltcore-dev/cortex/internal/extractor/plugins"
	"github.com/cobaltcore-dev/cortex/internal/sync/openstack/identity"
	"github.com/cobaltcore-dev/cortex/internal/sync/openstack/nova"
)

// Feature to calculate the resource (vcpu, ram, disk) utilization of all servers of projects with servers.
// Projects with no servers are not included in this table.
type ProjectResourceUtilization struct {
	// ID of the project.
	ProjectID string `db:"project_id"`
	// Availability zone of the servers.
	// This field can in theory be nil, but it should never be nil
	AvailabilityZone *string `db:"availability_zone"`
	// Total number of servers of the project.
	TotalServers int64 `db:"total_servers"`
	// Total number of servers of the project with a flavor that could not be resolved.
	UnresolvedServerFlavors int64 `db:"unresolved_server_flavors"`
	// Total number of vcpus used by all servers of the project.
	TotalVCPUsUsed int64 `db:"total_vcpus_used"`
	// Total amount of ram (in MB) used by all servers of the project.
	TotalRAMUsedMB int64 `db:"total_ram_used_mb"`
	// Total amount of disk (in GB) used by all servers of the project.
	TotalDiskUsedGB int64 `db:"total_disk_used_gb"`
}

// Table under which the feature is stored.
func (ProjectResourceUtilization) TableName() string {
	return "feature_project_resource_utilization"
}

// Indexes for the feature.
func (ProjectResourceUtilization) Indexes() []db.Index { return nil }

// Extractor that extracts the resource utilization of all servers of projects with servers.
type ProjectResourceUtilizationExtractor struct {
	// Common base for all extractors that provides standard functionality.
	plugins.BaseExtractor[
		struct{},                   // No options passed through yaml config
		ProjectResourceUtilization, // Feature model
	]
}

// Name of this feature extractor that is used in the yaml config, for logging etc.
func (*ProjectResourceUtilizationExtractor) GetName() string {
	return "project_resource_utilization_extractor"
}

// Get message topics that trigger a re-execution of this extractor.
func (ProjectResourceUtilizationExtractor) Triggers() []string {
	return []string{
		nova.TriggerNovaFlavorsSynced,
		nova.TriggerNovaServersSynced,
		identity.TriggerIdentityProjectsSynced,
	}
}

//go:embed project_resource_utilization.sql
var projectResourceUtilizationQuery string

// Extract the resource utilization of all servers of projects with servers.
func (e *ProjectResourceUtilizationExtractor) Extract() ([]plugins.Feature, error) {
	return e.ExtractSQL(projectResourceUtilizationQuery)
}
