// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package vmware

import (
	_ "embed"

	"github.com/cobaltcore-dev/cortex/extractor/api/features/vmware"
	"github.com/cobaltcore-dev/cortex/extractor/internal/plugins"
	"github.com/cobaltcore-dev/cortex/sync/api/objects/prometheus"
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

// Name of this feature extractor that is used in the yaml config, for logging etc.
func (*VROpsHostsystemContentionShortTermExtractor) GetName() string {
	return "vrops_hostsystem_contention_short_term_extractor"
}

// Get message topics that trigger a re-execution of this extractor.
func (VROpsHostsystemContentionShortTermExtractor) Triggers() []string {
	return []string{
		prometheus.TriggerMetricAliasSynced("vrops_hostsystem_cpu_contention_short_term_percentage"),
	}
}

//go:embed vrops_hostsystem_contention_short_term.sql
var vropsHostsystemContentionShortTermSQL string

// Extract short term CPU contention of hostsystems.
// Depends on resolved vROps hostsystems (feature_vrops_resolved_hostsystem).
func (e *VROpsHostsystemContentionShortTermExtractor) Extract() ([]plugins.Feature, error) {
	return e.ExtractSQL(vropsHostsystemContentionShortTermSQL)
}
