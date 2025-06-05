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

// Feature that maps the traits of a compute host in OpenStack.
type HostTraits struct {
	// Name of the OpenStack compute host.
	ComputeHost string `db:"compute_host"`
	// Comma-separated list of traits assigned to the compute host.
	Traits string `db:"traits"`
}

// Table under which the feature is stored.
func (HostTraits) TableName() string {
	return "feature_host_traits"
}

// Indexes for the feature.
func (HostTraits) Indexes() []db.Index {
	return []db.Index{
		{
			Name:        "idx_host_traits_compute_host",
			ColumnNames: []string{"compute_host"},
		},
	}
}

// Extractor that extracts the traits of a compute host.
type HostTraitsExtractor struct {
	// Common base for all extractors that provides standard functionality.
	plugins.BaseExtractor[
		struct{},   // No options passed through yaml config
		HostTraits, // Feature model
	]
}

// Name of this feature extractor that is used in the yaml config, for logging etc.
func (*HostTraitsExtractor) GetName() string {
	return "host_traits_extractor"
}

// Get message topics that trigger a re-execution of this extractor.
func (HostTraitsExtractor) Triggers() []string {
	return []string{
		nova.TriggerNovaHypervisorsSynced,
		placement.TriggerPlacementTraitsSynced,
	}
}

//go:embed host_traits.sql
var hostTraitsQuery string

// Extract the traits of a compute host from the database.
func (e *HostTraitsExtractor) Extract() ([]plugins.Feature, error) {
	return e.ExtractSQL(hostTraitsQuery)
}
