// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package vmware

import (
	_ "embed"

	"github.com/cobaltcore-dev/cortex/extractor/plugins"
	"github.com/cobaltcore-dev/cortex/internal/db"
	"github.com/cobaltcore-dev/cortex/internal/sync/prometheus"
)

// Feature that maps CPU contention of vROps hostsystems.
type VROpsHostsystemContention struct {
	ComputeHost      string  `db:"compute_host"`
	AvgCPUContention float64 `db:"avg_cpu_contention"`
	MaxCPUContention float64 `db:"max_cpu_contention"`
}

// Table under which the feature is stored.
func (VROpsHostsystemContention) TableName() string {
	return "feature_vrops_hostsystem_contention"
}

// Indexes for the feature.
func (VROpsHostsystemContention) Indexes() []db.Index {
	return nil
}

// Extractor that extracts CPU contention of vROps hostsystems and stores
// it as a feature into the database.
type VROpsHostsystemContentionExtractor struct {
	// Common base for all extractors that provides standard functionality.
	plugins.BaseExtractor[
		struct{},                  // No options passed through yaml config
		VROpsHostsystemContention, // Feature model
	]
}

// Name of this feature extractor that is used in the yaml config, for logging etc.
func (*VROpsHostsystemContentionExtractor) GetName() string {
	return "vrops_hostsystem_contention_extractor"
}

// Get message topics that trigger a re-execution of this extractor.
func (VROpsHostsystemContentionExtractor) Triggers() []string {
	return []string{
		prometheus.TriggerMetricAliasSynced("vrops_hostsystem_cpu_contention_percentage"),
	}
}

//go:embed vrops_hostsystem_contention.sql
var vropsHostsystemContentionSQL string

// Extract CPU contention of hostsystems.
// Depends on resolved vROps hostsystems (feature_vrops_resolved_hostsystem).
func (e *VROpsHostsystemContentionExtractor) Extract() ([]plugins.Feature, error) {
	return e.ExtractSQL(vropsHostsystemContentionSQL)
}
