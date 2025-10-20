// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package kvm

import (
	_ "embed"

	"github.com/cobaltcore-dev/cortex/knowledge/api/datasources/prometheus"
	"github.com/cobaltcore-dev/cortex/knowledge/api/features/kvm"
	"github.com/cobaltcore-dev/cortex/knowledge/internal/extractor/plugins"
)

// Extractor that extracts how much memory of kvm hosts is active and stores
// it as a feature into the database.
type NodeExporterHostMemoryActiveExtractor struct {
	// Common base for all extractors that provides standard functionality.
	plugins.BaseExtractor[
		struct{},                         // No options passed through yaml config
		kvm.NodeExporterHostMemoryActive, // Feature model
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
