// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package shared

import (
	_ "embed"

	"github.com/cobaltcore-dev/cortex/knowledge/api/features/shared"
	"github.com/cobaltcore-dev/cortex/knowledge/internal/extractor/plugins"
)

// Extractor that extracts the pinned projects of a compute host.
type HostPinnedProjectsExtractor struct {
	// Common base for all extractors that provides standard functionality.
	plugins.BaseExtractor[
		struct{},                  // No options passed through yaml config
		shared.HostPinnedProjects, // Feature model
	]
}

//go:embed host_pinned_projects.sql
var hostPinnedProjectsQuery string

// Extract the pinned projects of a compute host.
func (e *HostPinnedProjectsExtractor) Extract() ([]plugins.Feature, error) {
	return e.ExtractSQL(hostPinnedProjectsQuery)
}
