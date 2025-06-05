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
type HostCapabilities struct {
	// Name of the OpenStack compute host.
	ComputeHost string `db:"compute_host"`
	// Comma-separated list of traits assigned to the compute host.
	Traits string `db:"traits"`
	// The type of hypervisor running on the compute host.
	HypervisorType string `db:"hypervisor_type"`
}

// Table under which the feature is stored.
func (HostCapabilities) TableName() string {
	return "feature_host_capabilities"
}

// Indexes for the feature.
func (HostCapabilities) Indexes() []db.Index {
	return []db.Index{
		{
			Name:        "idx_host_capabilities_compute_host",
			ColumnNames: []string{"compute_host"},
		},
	}
}

// Extractor that extracts the traits of a compute host.
type HostCapabilitiesExtractor struct {
	// Common base for all extractors that provides standard functionality.
	plugins.BaseExtractor[
		struct{},         // No options passed through yaml config
		HostCapabilities, // Feature model
	]
}

// Name of this feature extractor that is used in the yaml config, for logging etc.
func (*HostCapabilitiesExtractor) GetName() string {
	return "host_capabilities_extractor"
}

// Get message topics that trigger a re-execution of this extractor.
func (HostCapabilitiesExtractor) Triggers() []string {
	return []string{
		nova.TriggerNovaHypervisorsSynced,
		placement.TriggerPlacementTraitsSynced,
	}
}

//go:embed host_capabilities.sql
var hostCapabilitiesQuery string

// Extract the traits of a compute host from the database.
func (e *HostCapabilitiesExtractor) Extract() ([]plugins.Feature, error) {
	return e.ExtractSQL(hostCapabilitiesQuery)
}
