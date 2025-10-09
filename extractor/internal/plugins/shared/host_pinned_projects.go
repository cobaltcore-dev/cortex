// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package shared

import (
	_ "embed"

	"github.com/cobaltcore-dev/cortex/extractor/api/features/shared"
	"github.com/cobaltcore-dev/cortex/extractor/internal/plugins"
	"github.com/cobaltcore-dev/cortex/sync/api/objects/openstack/nova"
)

// Extractor that extracts the pinned projects of a compute host.
type HostPinnedProjectsExtractor struct {
	// Common base for all extractors that provides standard functionality.
	plugins.BaseExtractor[
		struct{},                  // No options passed through yaml config
		shared.HostPinnedProjects, // Feature model
	]
}

// Name of this feature extractor that is used in the yaml config, for logging etc.
func (*HostPinnedProjectsExtractor) GetName() string {
	return "host_pinned_projects_extractor"
}

// Get message topics that trigger a re-execution of this extractor.
func (HostPinnedProjectsExtractor) Triggers() []string {
	return []string{
		nova.TriggerNovaHypervisorsSynced,
		nova.TriggerNovaAggregatesSynced,
	}
}

//go:embed host_pinned_projects.sql
var hostPinnedProjectsQuery string

// Extract the pinned projects of a compute host.
func (e *HostPinnedProjectsExtractor) Extract() ([]plugins.Feature, error) {
	return e.ExtractSQL(hostPinnedProjectsQuery)
}
