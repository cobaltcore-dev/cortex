// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package shared

import (
	_ "embed"

	"github.com/cobaltcore-dev/cortex/extractor/api/features/shared"
	"github.com/cobaltcore-dev/cortex/extractor/internal/plugins"
	"github.com/cobaltcore-dev/cortex/sync/api/objects/openstack/nova"
	"github.com/cobaltcore-dev/cortex/sync/api/objects/openstack/placement"
)

// Extractor that extracts the traits of a compute host.
type HostCapabilitiesExtractor struct {
	// Common base for all extractors that provides standard functionality.
	plugins.BaseExtractor[
		struct{},                // No options passed through yaml config
		shared.HostCapabilities, // Feature model
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
