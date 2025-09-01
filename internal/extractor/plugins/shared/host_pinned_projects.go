// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package shared

import (
	_ "embed"

	"github.com/cobaltcore-dev/cortex/internal/db"
	"github.com/cobaltcore-dev/cortex/internal/extractor/plugins"
	"github.com/cobaltcore-dev/cortex/internal/sync/openstack/identity"
	"github.com/cobaltcore-dev/cortex/internal/sync/openstack/nova"
	"github.com/cobaltcore-dev/cortex/internal/sync/openstack/placement"
)

// Feature that maps how many resources are utilized on a compute host.
type HostPinnedProjects struct {
	// Name of the OpenStack compute host.
	ComputeHost string `db:"compute_host"`
	// Project ID
	ProjectID string `db:"project_id"`
}

// Table under which the feature is stored.
func (HostPinnedProjects) TableName() string {
	return "feature_host_pinned_projects"
}

// Indexes for the feature.
func (HostPinnedProjects) Indexes() []db.Index { return nil }

// Extractor that extracts the utilization on a compute host.
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
		placement.TriggerPlacementInventoryUsagesSynced,
		identity.TriggerIdentityDomainsSynced,
		identity.TriggerIdentityProjectsSynced,
	}
}

//go:embed host_pinned_projects.sql
var hostPinnedProjectsQuery string

// Extract the domains and projects on a compute host.
func (e *HostPinnedProjectsExtractor) Extract() ([]plugins.Feature, error) {
	return e.ExtractSQL(hostPinnedProjectsQuery)
}
