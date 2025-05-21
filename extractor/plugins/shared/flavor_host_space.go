// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package shared

import (
	_ "embed"

	"github.com/cobaltcore-dev/cortex/extractor/plugins"
	"github.com/cobaltcore-dev/cortex/internal/db"
	"github.com/cobaltcore-dev/cortex/internal/sync/openstack"
)

// Feature that maps the space left on a compute host after the placement of a flavor.
type FlavorHostSpace struct {
	// ID of the OpenStack flavor.
	FlavorID string `db:"flavor_id"`
	// Name of the OpenStack compute host.
	ComputeHost string `db:"compute_host"`
	// RAM left after the placement of the flavor.
	RAMLeftMB int `db:"ram_left_mb"`
	// CPU left after the placement of the flavor.
	VCPUsLeft int `db:"vcpus_left"`
	// Disk left after the placement of the flavor.
	DiskLeftGB int `db:"disk_left_gb"`
}

// Table under which the feature is stored.
func (FlavorHostSpace) TableName() string {
	return "feature_flavor_host_space"
}

// Indexes for the feature.
func (FlavorHostSpace) Indexes() []db.Index {
	return []db.Index{
		{
			Name:        "idx_flavor_host_space_flavor_id",
			ColumnNames: []string{"flavor_id"},
		},
	}
}

// Extractor that extracts the space left on a compute host after the placement
// of a flavor and stores it as a feature into the database.
type FlavorHostSpaceExtractor struct {
	// Common base for all extractors that provides standard functionality.
	plugins.BaseExtractor[
		struct{},        // No options passed through yaml config
		FlavorHostSpace, // Feature model
	]
}

// Name of this feature extractor that is used in the yaml config, for logging etc.
func (*FlavorHostSpaceExtractor) GetName() string {
	return "flavor_host_space_extractor"
}

// Get message topics that trigger a re-execution of this extractor.
func (FlavorHostSpaceExtractor) Triggers() []string {
	return []string{
		openstack.TriggerNovaFlavorsSynced,
		openstack.TriggerNovaHypervisorsSynced,
	}
}

//go:embed flavor_host_space.sql
var flavorHostSpaceQuery string

// Extract the space left on a compute host after the placement of a flavor.
// Depends on the OpenStack flavors and hypervisors to be synced.
func (e *FlavorHostSpaceExtractor) Extract() ([]plugins.Feature, error) {
	return e.ExtractSQL(flavorHostSpaceQuery)
}
