// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package kvm

import (
	_ "embed"

	"github.com/cobaltcore-dev/cortex/knowledge/api/datasources/prometheus"
	"github.com/cobaltcore-dev/cortex/knowledge/api/features/kvm"
	"github.com/cobaltcore-dev/cortex/knowledge/internal/extractor/plugins"
)

// Extractor that extracts CPU usage of kvm hosts and stores
// it as a feature into the database.
type NodeExporterHostCPUUsageExtractor struct {
	// Common base for all extractors that provides standard functionality.
	plugins.BaseExtractor[
		struct{},                     // No options passed through yaml config
		kvm.NodeExporterHostCPUUsage, // Feature model
	]
}

// Name of this feature extractor that is used in the yaml config, for logging etc.
func (*NodeExporterHostCPUUsageExtractor) GetName() string {
	return "node_exporter_host_cpu_usage_extractor"
}

// Get message topics that trigger a re-execution of this extractor.
func (NodeExporterHostCPUUsageExtractor) Triggers() []string {
	return []string{
		prometheus.TriggerMetricAliasSynced("node_exporter_cpu_usage_pct"),
	}
}

//go:embed node_exporter_host_cpu_usage.sql
var nodeExporterHostCPUUsageSQL string

// Extract CPU usage of kvm hosts.
func (e *NodeExporterHostCPUUsageExtractor) Extract() ([]plugins.Feature, error) {
	return e.ExtractSQL(nodeExporterHostCPUUsageSQL)
}
