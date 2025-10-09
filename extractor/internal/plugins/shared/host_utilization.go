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

// Extractor that extracts the utilization on a compute host.
type HostUtilizationExtractor struct {
	// Common base for all extractors that provides standard functionality.
	plugins.BaseExtractor[
		struct{},               // No options passed through yaml config
		shared.HostUtilization, // Feature model
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
