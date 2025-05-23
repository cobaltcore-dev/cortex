// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package kvm

import (
	_ "embed"

	"github.com/cobaltcore-dev/cortex/internal/db"
	"github.com/cobaltcore-dev/cortex/internal/extractor/plugins"
	"github.com/cobaltcore-dev/cortex/internal/sync/prometheus"
)

// Feature that maps memory active percentage of kvm hosts.
type NodeExporterHostMemoryActive struct {
	ComputeHost     string  `db:"compute_host"`
	AvgMemoryActive float64 `db:"avg_memory_active"`
	MaxMemoryActive float64 `db:"max_memory_active"`
}

// Table under which the feature is stored.
func (NodeExporterHostMemoryActive) TableName() string {
	return "feature_host_memory_active"
}

// Indexes for the feature.
func (NodeExporterHostMemoryActive) Indexes() []db.Index {
	return nil
}

// Extractor that extracts how much memory of kvm hosts is active and stores
// it as a feature into the database.
type NodeExporterHostMemoryActiveExtractor struct {
	// Common base for all extractors that provides standard functionality.
	plugins.BaseExtractor[
		struct{},                     // No options passed through yaml config
		NodeExporterHostMemoryActive, // Feature model
	]
}

// Name of this feature extractor that is used in the yaml config, for logging etc.
func (*NodeExporterHostMemoryActiveExtractor) GetName() string {
	return "node_exporter_host_memory_active_extractor"
}

// Get message topics that trigger a re-execution of this extractor.
func (NodeExporterHostMemoryActiveExtractor) Triggers() []string {
	return []string{
		prometheus.TriggerMetricAliasSynced("node_exporter_memory_active_pct"),
	}
}

//go:embed node_exporter_host_memory_active.sql
var nodeExporterHostMemoryActiveSQL string

// Extract how much memory of kvm hosts is active.
func (e *NodeExporterHostMemoryActiveExtractor) Extract() ([]plugins.Feature, error) {
	return e.ExtractSQL(nodeExporterHostMemoryActiveSQL)
}
