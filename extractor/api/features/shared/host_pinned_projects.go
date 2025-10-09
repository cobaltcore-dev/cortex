// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package shared

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
func (HostPinnedProjects) Indexes() map[string][]string { return nil }
