// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package vmware

import (
	"github.com/cobaltcore-dev/cortex/internal/features/plugins"
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

// Extract CPU contention of hostsystems.
// Depends on resolved vROps hostsystems (feature_vrops_resolved_hostsystem).
func (e *VROpsHostsystemContentionExtractor) Extract() ([]plugins.Feature, error) {
	var features []VROpsHostsystemContention
	if _, err := e.DB.Select(&features, `
		SELECT
			h.nova_compute_host AS compute_host,
			AVG(m.value) AS avg_cpu_contention,
			MAX(m.value) AS max_cpu_contention
		FROM vrops_host_metrics m
		JOIN feature_vrops_resolved_hostsystem h ON m.hostsystem = h.vrops_hostsystem
		WHERE m.name = 'vrops_hostsystem_cpu_contention_percentage'
		GROUP BY h.nova_compute_host;
    `); err != nil {
		return nil, err
	}
	return e.Extracted(features)
}
