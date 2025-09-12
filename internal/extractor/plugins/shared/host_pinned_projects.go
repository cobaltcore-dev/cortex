// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package shared

import (
	_ "embed"

	"github.com/cobaltcore-dev/cortex/internal/db"
	"github.com/cobaltcore-dev/cortex/internal/extractor/plugins"
	"github.com/cobaltcore-dev/cortex/internal/sync/openstack/nova"
)

// HostPinnedProjects represents the mapping between compute hosts and their tenant restrictions.
// This feature tracks which projects are allowed on specific hosts based on Nova aggregate
// tenant isolation filters. Hosts without restrictions have a NULL project_id, indicating
// they accept workloads from any project.
// See the docs: https://docs.openstack.org/nova/latest/admin/scheduling.html#aggregatemultitenancyisolation
type HostPinnedProjects struct {
	// The name of the aggregate where the filter is defined
	AggregateName *string `db:"aggregate_name"`
	// UUID of the aggregate where the filter is defined
	AggregateUUID *string `db:"aggregate_uuid"`
	// Tenant ID that belongs to the filter
	ProjectID *string `db:"project_id"`
	// Name of the OpenStack compute host.
	ComputeHost *string `db:"compute_host"`
}

// Table under which the feature is stored.
func (HostPinnedProjects) TableName() string {
	return "feature_host_pinned_projects"
}

// Indexes for the feature.
func (HostPinnedProjects) Indexes() []db.Index { return nil }

// Extractor that extracts the pinned projects of a compute host.
type HostPinnedProjectsExtractor struct {
	// Common base for all extractors that provides standard functionality.
	plugins.BaseExtractor[
		struct{},           // No options passed through yaml config
		HostPinnedProjects, // Feature model
	]
}

// Name of this feature extractor that is used in the yaml config, for logging etc.
func (*HostPinnedProjectsExtractor) GetName() string {
	return "host_pinned_projects_extractor"
}

// Get message topics that trigger a re-execution of this extractor.
func (HostPinnedProjectsExtractor) Triggers() []string {
	return []string{
		nova.TriggerNovaHypervisorsSynced,
		nova.TriggerNovaAggregatesSynced,
	}
}

//go:embed host_pinned_projects.sql
var hostPinnedProjectsQuery string

// Extract the pinned projects of a compute host.
func (e *HostPinnedProjectsExtractor) Extract() ([]plugins.Feature, error) {
	return e.ExtractSQL(hostPinnedProjectsQuery)
}
