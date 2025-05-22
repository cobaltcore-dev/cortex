// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package vmware

import (
	_ "embed"

	"github.com/cobaltcore-dev/cortex/internal/db"
	"github.com/cobaltcore-dev/cortex/internal/features/plugins"
	"github.com/cobaltcore-dev/cortex/internal/sync/prometheus"
)

// Feature that maps CPU contention of vROps hostsystems.
type VROpsHostsystemContentionLongTerm struct {
	ComputeHost      string  `db:"compute_host"`
	AvgCPUContention float64 `db:"avg_cpu_contention"`
	MaxCPUContention float64 `db:"max_cpu_contention"`
}

// Table under which the feature is stored.
func (VROpsHostsystemContentionLongTerm) TableName() string {
	return "feature_vrops_hostsystem_contention_long_term"
}

// Indexes for the feature.
func (VROpsHostsystemContentionLongTerm) Indexes() []db.Index {
	return nil
}

// Extractor that extracts CPU contention of vROps hostsystems and stores
// it as a feature into the database.
type VROpsHostsystemContentionLongTermExtractor struct {
	// Common base for all extractors that provides standard functionality.
	plugins.BaseExtractor[
		struct{},                          // No options passed through yaml config
		VROpsHostsystemContentionLongTerm, // Feature model
	]
}

// Name of this feature extractor that is used in the yaml config, for logging etc.
func (*VROpsHostsystemContentionLongTermExtractor) GetName() string {
	return "vrops_hostsystem_contention_long_term_extractor"
}

// Get message topics that trigger a re-execution of this extractor.
func (VROpsHostsystemContentionLongTermExtractor) Triggers() []string {
	return []string{
		prometheus.TriggerMetricAliasSynced("vrops_hostsystem_cpu_contention_long_term_percentage"),
	}
}

//go:embed vrops_hostsystem_contention_long_term.sql
var vropsHostsystemContentionLongTermSQL string

// Extract long term CPU contention of hostsystems.
// Depends on resolved vROps hostsystems (feature_vrops_resolved_hostsystem).
func (e *VROpsHostsystemContentionLongTermExtractor) Extract() ([]plugins.Feature, error) {
	return e.ExtractSQL(vropsHostsystemContentionLongTermSQL)
}
