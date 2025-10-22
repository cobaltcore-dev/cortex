// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package vmware

import (
	_ "embed"

	"github.com/cobaltcore-dev/cortex/knowledge/api/features/vmware"
	"github.com/cobaltcore-dev/cortex/knowledge/internal/extractor/plugins"
)

// Extractor that extracts CPU contention of vROps hostsystems and stores
// it as a feature into the database.
type VROpsHostsystemContentionShortTermExtractor struct {
	// Common base for all extractors that provides standard functionality.
	plugins.BaseExtractor[
		struct{}, // No options passed through yaml config
		vmware.VROpsHostsystemContentionShortTerm, // Feature model
	]
}

//go:embed vrops_hostsystem_contention_short_term.sql
var vropsHostsystemContentionShortTermSQL string

// Extract short term CPU contention of hostsystems.
// Depends on resolved vROps hostsystems (feature_vrops_resolved_hostsystem).
func (e *VROpsHostsystemContentionShortTermExtractor) Extract() ([]plugins.Feature, error) {
	return e.ExtractSQL(vropsHostsystemContentionShortTermSQL)
}
