// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package kvm

import (
	"github.com/cobaltcore-dev/cortex/internal/features/plugins"
)

// Feature that maps CPU usage of kvm hosts.
type NodeExporterHostCPUUsage struct {
	ComputeHost string  `db:"compute_host"`
	AvgCPUUsage float64 `db:"avg_cpu_usage"`
	MaxCPUUsage float64 `db:"max_cpu_usage"`
}

// Table under which the feature is stored.
func (NodeExporterHostCPUUsage) TableName() string {
	return "feature_host_cpu_usage"
}

// Extractor that extracts CPU usage of kvm hosts and stores
// it as a feature into the database.
type NodeExporterHostCPUUsageExtractor struct {
	// Common base for all extractors that provides standard functionality.
	plugins.BaseExtractor[
		struct{},                 // No options passed through yaml config
		NodeExporterHostCPUUsage, // Feature model
	]
}

// Name of this feature extractor that is used in the yaml config, for logging etc.
func (*NodeExporterHostCPUUsageExtractor) GetName() string {
	return "node_exporter_host_cpu_usage_extractor"
}

// Extract CPU usage of kvm hosts.
func (e *NodeExporterHostCPUUsageExtractor) Extract() ([]plugins.Feature, error) {
	var features []NodeExporterHostCPUUsage
	if _, err := e.DB.Select(&features, `
		SELECT
			node AS compute_host,
			AVG(value) AS avg_cpu_usage,
			MAX(value) AS max_cpu_usage
		FROM node_exporter_metrics
		WHERE name = 'node_exporter_cpu_usage_pct'
		GROUP BY node;
	`); err != nil {
		return nil, err
	}
	return e.Extracted(features)
}
