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

// Feature that maps how many resources are utilized on a compute host.
type HostDomainProject struct {
	// Name of the OpenStack compute host.
	ComputeHost string `db:"compute_host"`
	// Names of projects that are running on this compute host.
	ProjectName string `db:"project_name"`
	// IDs of projects that are running on this compute host.
	ProjectID string `db:"project_id"`
	// Names of domains that are running on this compute host.
	DomainName string `db:"domain_name"`
	// IDs of domains that are running on this compute host.
	DomainID string `db:"domain_id"`
}

// Table under which the feature is stored.
func (HostDomainProject) TableName() string {
	return "feature_host_projects"
}

// Indexes for the feature.
func (HostDomainProject) Indexes() []db.Index { return nil }

// Extractor that extracts the utilization on a compute host.
type HostDomainProjectExtractor struct {
	// Common base for all extractors that provides standard functionality.
	plugins.BaseExtractor[
		struct{},          // No options passed through yaml config
		HostDomainProject, // Feature model
	]
}

// Name of this feature extractor that is used in the yaml config, for logging etc.
func (*HostDomainProjectExtractor) GetName() string {
	return "host_projects_extractor"
}

// Get message topics that trigger a re-execution of this extractor.
func (HostDomainProjectExtractor) Triggers() []string {
	return []string{
		nova.TriggerNovaHypervisorsSynced,
		identity.TriggerIdentityDomainsSynced,
		identity.TriggerIdentityProjectsSynced,
	}
}

//go:embed host_projects.sql
var hostDomainProjectQuery string

// Extract the domains and projects on a compute host.
func (e *HostDomainProjectExtractor) Extract() ([]plugins.Feature, error) {
	return e.ExtractSQL(hostDomainProjectQuery)
}
