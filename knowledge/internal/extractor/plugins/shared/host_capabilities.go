// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package shared

import (
	_ "embed"

	"github.com/cobaltcore-dev/cortex/knowledge/api/features/shared"
	"github.com/cobaltcore-dev/cortex/knowledge/internal/extractor/plugins"
)

// Extractor that extracts the traits of a compute host.
type HostCapabilitiesExtractor struct {
	// Common base for all extractors that provides standard functionality.
	plugins.BaseExtractor[
		struct{},                // No options passed through yaml config
		shared.HostCapabilities, // Feature model
	]
}

//go:embed host_capabilities.sql
var hostCapabilitiesQuery string

// Extract the traits of a compute host from the database.
func (e *HostCapabilitiesExtractor) Extract() ([]plugins.Feature, error) {
	return e.ExtractSQL(hostCapabilitiesQuery)
}
