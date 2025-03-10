// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package shared

import (
	"github.com/cobaltcore-dev/cortex/internal/db"
	"github.com/cobaltcore-dev/cortex/internal/features/plugins"
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

// Extract the space left on a compute host after the placement of a flavor.
// Depends on the OpenStack flavors and hypervisors to be synced.
func (e *FlavorHostSpaceExtractor) Extract() ([]plugins.Feature, error) {
	var hypervisors []openstack.Hypervisor
	query := "SELECT * FROM " + openstack.Hypervisor{}.TableName()
	if _, err := e.DB.Select(&hypervisors, query); err != nil {
		return nil, err
	}
	var flavors []openstack.Flavor
	query = "SELECT * FROM " + openstack.Flavor{}.TableName()
	if _, err := e.DB.Select(&flavors, query); err != nil {
		return nil, err
	}
	var features []FlavorHostSpace
	for _, h := range hypervisors {
		for _, f := range flavors {
			features = append(features, FlavorHostSpace{
				FlavorID:    f.ID,
				ComputeHost: h.ServiceHost,
				RAMLeftMB:   h.FreeRAMMB - f.RAM,
				VCPUsLeft:   h.VCPUs - h.VCPUsUsed - f.VCPUs,
				DiskLeftGB:  h.FreeDiskGB - f.Disk,
			})
		}
	}
	if err := db.ReplaceAll(e.DB, features...); err != nil {
		return nil, err
	}
	return e.Extracted(features)
}
