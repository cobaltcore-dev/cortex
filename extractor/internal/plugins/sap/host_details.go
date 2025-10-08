// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package sap

import (
	_ "embed"

	"github.com/cobaltcore-dev/cortex/extractor/api/features/sap"
	"github.com/cobaltcore-dev/cortex/extractor/internal/plugins"
	"github.com/cobaltcore-dev/cortex/internal/sync/openstack/nova"
	"github.com/cobaltcore-dev/cortex/internal/sync/openstack/placement"
)

type HostDetailsExtractor struct {
	// Common base for all extractors that provides standard functionality.
	plugins.BaseExtractor[
		struct{},        // No options passed through yaml config
		sap.HostDetails, // Feature model
	]
}

// Name of this feature extractor that is used in the yaml config, for logging etc.
func (*HostDetailsExtractor) GetName() string {
	return "sap_host_details_extractor"
}

// Get message topics that trigger a re-execution of this extractor.
func (HostDetailsExtractor) Triggers() []string {
	return []string{
		placement.TriggerPlacementTraitsSynced,
		nova.TriggerNovaHypervisorsSynced,
	}
}

//go:embed host_details.sql
var hostDetailsQuery string

// Extract the traits of a compute host from the database.
func (e *HostDetailsExtractor) Extract() ([]plugins.Feature, error) {
	return e.ExtractSQL(hostDetailsQuery)
}
