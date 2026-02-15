// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package compute

import (
	_ "embed"

	"github.com/cobaltcore-dev/cortex/api/v1alpha1"
	"github.com/cobaltcore-dev/cortex/internal/knowledge/extractor/plugins"
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
	// Domain ID of the project
	DomainID *string `db:"domain_id"`
	// Combination of project name and domain name
	Label *string `db:"label"`
	// Name of the OpenStack compute host.
	ComputeHost *string `db:"compute_host"`
}

// Extractor that extracts the pinned projects of a compute host.
type HostPinnedProjectsExtractor struct {
	// Common base for all extractors that provides standard functionality.
	plugins.BaseExtractor[
		struct{},           // No options passed through yaml config
		HostPinnedProjects, // Feature model
	]
}

//go:embed host_pinned_projects.sql
var hostPinnedProjectsQuery string

// Extract the pinned projects of a compute host.
func (e *HostPinnedProjectsExtractor) Extract(_ []*v1alpha1.Datasource, _ []*v1alpha1.Knowledge) ([]plugins.Feature, error) {
	return e.ExtractSQL(hostPinnedProjectsQuery)
}
