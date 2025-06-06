// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package shared

import (
	_ "embed"

	"github.com/cobaltcore-dev/cortex/internal/db"
	"github.com/cobaltcore-dev/cortex/internal/extractor/plugins"
	"github.com/cobaltcore-dev/cortex/internal/sync/openstack/nova"
)

// Feature that maps the space left on a compute host.
type HostSpace struct {
	// Name of the OpenStack compute host.
	ComputeHost string `db:"compute_host"`
	// RAM left in MB.
	RAMLeftMB int `db:"ram_left_mb"`
	// RAM left in pct.
	RAMLeftPct float64 `db:"ram_left_pct"`
	// CPU left in vCPUs.
	VCPUsLeft int `db:"vcpus_left"`
	// CPU left in pct.
	VCPUsLeftPct float64 `db:"vcpus_left_pct"`
	// Disk left in GB.
	DiskLeftGB int `db:"disk_left_gb"`
	// Disk left in pct.
	DiskLeftPct float64 `db:"disk_left_pct"`
}

// Table under which the feature is stored.
func (HostSpace) TableName() string {
	return "feature_host_space"
}

// Indexes for the feature.
func (HostSpace) Indexes() []db.Index {
	return []db.Index{
		{
			Name:        "idx_host_space_compute_host",
			ColumnNames: []string{"compute_host"},
		},
	}
}

// Extractor that extracts the space left on a compute host.
type HostSpaceExtractor struct {
	// Common base for all extractors that provides standard functionality.
	plugins.BaseExtractor[
		struct{},  // No options passed through yaml config
		HostSpace, // Feature model
	]
}

// Name of this feature extractor that is used in the yaml config, for logging etc.
func (*HostSpaceExtractor) GetName() string {
	return "host_space_extractor"
}

// Get message topics that trigger a re-execution of this extractor.
func (HostSpaceExtractor) Triggers() []string {
	return []string{
		nova.TriggerNovaHypervisorsSynced,
	}
}

//go:embed host_space.sql
var hostSpaceQuery string

// Extract the space left on a compute host.
// Depends on the OpenStack hypervisors to be synced.
func (e *HostSpaceExtractor) Extract() ([]plugins.Feature, error) {
	return e.ExtractSQL(hostSpaceQuery)
}
