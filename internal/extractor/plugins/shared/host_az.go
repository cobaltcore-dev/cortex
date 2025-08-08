// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package shared

import (
	_ "embed"

	"github.com/cobaltcore-dev/cortex/internal/db"
	"github.com/cobaltcore-dev/cortex/internal/extractor/plugins"
	"github.com/cobaltcore-dev/cortex/internal/sync/openstack/nova"
)

type HostAZ struct {
	// Name of the OpenStack compute host.
	ComputeHost string `db:"compute_host"`
	// Availability zone of the compute host, if available.
	AvailabilityZone *string `db:"availability_zone"`
}

// Table under which the feature is stored.
func (HostAZ) TableName() string {
	return "feature_host_az"
}

// Indexes for the feature.
func (HostAZ) Indexes() []db.Index {
	return []db.Index{
		{
			Name:        "idx_host_az_compute_host",
			ColumnNames: []string{"compute_host"},
		},
	}
}

type HostAZExtractor struct {
	// Common base for all extractors that provides standard functionality.
	plugins.BaseExtractor[
		struct{}, // No options passed through yaml config
		HostAZ,   // Feature model
	]
}

// Name of this feature extractor that is used in the yaml config, for logging etc.
func (*HostAZExtractor) GetName() string {
	return "host_az_extractor"
}

// Get message topics that trigger a re-execution of this extractor.
func (HostAZExtractor) Triggers() []string {
	return []string{
		nova.TriggerNovaAggregatesSynced,
		nova.TriggerNovaHypervisorsSynced,
	}
}

//go:embed host_az.sql
var hostAZQuery string

// Extract the traits of a compute host from the database.
func (e *HostAZExtractor) Extract() ([]plugins.Feature, error) {
	return e.ExtractSQL(hostAZQuery)
}
