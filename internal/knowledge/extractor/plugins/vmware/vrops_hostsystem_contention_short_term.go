// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package vmware

import (
	_ "embed"

	"github.com/cobaltcore-dev/cortex/internal/knowledge/extractor/plugins"
)

// Feature that maps CPU contention of vROps hostsystems.
type VROpsHostsystemContentionShortTerm struct {
	ComputeHost      string  `db:"compute_host"`
	AvgCPUContention float64 `db:"avg_cpu_contention"`
	MaxCPUContention float64 `db:"max_cpu_contention"`
}

// Table under which the feature is stored.
func (VROpsHostsystemContentionShortTerm) TableName() string {
	return "feature_vrops_hostsystem_contention_short_term"
}

// Indexes for the feature.
func (VROpsHostsystemContentionShortTerm) Indexes() map[string][]string { return nil }

// Extractor that extracts CPU contention of vROps hostsystems and stores
// it as a feature into the database.
type VROpsHostsystemContentionShortTermExtractor struct {
	// Common base for all extractors that provides standard functionality.
	plugins.BaseExtractor[
		struct{},                           // No options passed through yaml config
		VROpsHostsystemContentionShortTerm, // Feature model
	]
}

//go:embed vrops_hostsystem_contention_short_term.sql
var vropsHostsystemContentionShortTermSQL string

// Extract short term CPU contention of hostsystems.
// Depends on resolved vROps hostsystems (feature_vrops_resolved_hostsystem).
func (e *VROpsHostsystemContentionShortTermExtractor) Extract() ([]plugins.Feature, error) {
	return e.ExtractSQL(vropsHostsystemContentionShortTermSQL)
}
