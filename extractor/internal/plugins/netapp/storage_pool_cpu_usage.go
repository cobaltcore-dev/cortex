// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package netapp

import (
	_ "embed"

	"github.com/cobaltcore-dev/cortex/extractor/api/features/netapp"
	"github.com/cobaltcore-dev/cortex/extractor/internal/plugins"
	"github.com/cobaltcore-dev/cortex/internal/sync/openstack/manila"
	"github.com/cobaltcore-dev/cortex/internal/sync/prometheus"
)

// Extractor that extracts the CPU usage of a storage pool.
type StoragePoolCPUUsageExtractor struct {
	// Common base for all extractors that provides standard functionality.
	plugins.BaseExtractor[
		struct{},                   // No options passed through yaml config
		netapp.StoragePoolCPUUsage, // Feature model
	]
}

// Name of this feature extractor that is used in the yaml config, for logging etc.
func (*StoragePoolCPUUsageExtractor) GetName() string {
	return "netapp_storage_pool_cpu_usage_extractor"
}

// Get message topics that trigger a re-execution of this extractor.
func (StoragePoolCPUUsageExtractor) Triggers() []string {
	return []string{
		manila.TriggerManilaStoragePoolsSynced,
		prometheus.TriggerMetricTypeSynced("netapp_aggregate_labels_metric"),
		prometheus.TriggerMetricAliasSynced("netapp_node_cpu_busy"),
	}
}

//go:embed storage_pool_cpu_usage.sql
var storagePoolCPUUsageQuery string

// Extract the CPU usage of a storage pool.
func (e *StoragePoolCPUUsageExtractor) Extract() ([]plugins.Feature, error) {
	return e.ExtractSQL(storagePoolCPUUsageQuery)
}
