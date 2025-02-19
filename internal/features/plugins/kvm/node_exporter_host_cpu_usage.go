// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package kvm

import (
	"log/slog"

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
// Depends on resolved kvm hosts (feature_resolved_host).
func (e *NodeExporterHostCPUUsageExtractor) Extract() ([]plugins.Feature, error) {
	// Delete the old data in the same transaction.
	tx, err := e.DB.Begin()
	if err != nil {
		return nil, err
	}
	if _, err := tx.Exec("DELETE FROM feature_host_cpu_usage"); err != nil {
		return nil, tx.Rollback()
	}
	if _, err := tx.Exec(`
		INSERT INTO feature_host_cpu_usage (compute_host, avg_cpu_usage, max_cpu_usage)
		SELECT
			node AS compute_host,
			AVG(value) AS avg_cpu_usage,
			MAX(value) AS max_cpu_usage
		FROM node_exporter_metrics
		WHERE name = 'node_exporter_cpu_usage_pct'
		GROUP BY node;
	`); err != nil {
		return nil, tx.Rollback()
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	// Load the extracted features from the database and return them.
	var features []NodeExporterHostCPUUsage
	if _, err := e.DB.Select(&features, "SELECT * FROM feature_host_cpu_usage"); err != nil {
		return nil, err
	}
	output := make([]plugins.Feature, len(features))
	for i, f := range features {
		output[i] = f
	}
	slog.Info("features: extracted", "feature_host_cpu_usage", len(output))
	return output, nil
}
