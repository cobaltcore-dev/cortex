// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package shared

import (
	_ "embed"

	"github.com/cobaltcore-dev/cortex/internal/db"
	"github.com/cobaltcore-dev/cortex/internal/extractor/plugins"
	"github.com/cobaltcore-dev/cortex/internal/sync/openstack/nova"
	"github.com/cobaltcore-dev/cortex/internal/sync/openstack/placement"
)

// Feature that maps how many resources are utilized on a compute host.
type HostUtilization struct {
	// Name of the OpenStack compute host.
	ComputeHost string `db:"compute_host"`
	// RAM utilized in pct.
	RAMUtilizedPct float64 `db:"ram_utilized_pct"`
	// CPU utilized in pct.
	VCPUsUtilizedPct float64 `db:"vcpus_utilized_pct"`
	// Disk utilized in pct.
	DiskUtilizedPct float64 `db:"disk_utilized_pct"`
	// Total memory allocatable (including overcommit) in MB.
	TotalMemoryAllocatableMB float64 `db:"total_memory_allocatable_mb"`
	// Total vCPUs allocatable (including overcommit).
	TotalVCPUsAllocatable float64 `db:"total_vcpus_allocatable"`
	// Total disk allocatable (including overcommit) in GB.
	TotalDiskAllocatableGB float64 `db:"total_disk_allocatable_gb"`
}

// Table under which the feature is stored.
func (HostUtilization) TableName() string {
	return "feature_host_utilization"
}

// Indexes for the feature.
func (HostUtilization) Indexes() []db.Index {
	return []db.Index{
		{
			Name:        "idx_host_utilization_compute_host",
			ColumnNames: []string{"compute_host"},
		},
	}
}

// Extractor that extracts the utilization on a compute host.
type HostUtilizationExtractor struct {
	// Common base for all extractors that provides standard functionality.
	plugins.BaseExtractor[
		struct{},        // No options passed through yaml config
		HostUtilization, // Feature model
	]
}

// Name of this feature extractor that is used in the yaml config, for logging etc.
func (*HostUtilizationExtractor) GetName() string {
	return "host_utilization_extractor"
}

// Get message topics that trigger a re-execution of this extractor.
func (HostUtilizationExtractor) Triggers() []string {
	return []string{
		nova.TriggerNovaHypervisorsSynced,
		placement.TriggerPlacementInventoryUsagesSynced,
	}
}

//go:embed host_utilization.sql
var hostUtilizationQuery string

// Extract the utilization on a compute host.
// Depends on the OpenStack hypervisors to be synced.
func (e *HostUtilizationExtractor) Extract() ([]plugins.Feature, error) {
	return e.ExtractSQL(hostUtilizationQuery)
}
