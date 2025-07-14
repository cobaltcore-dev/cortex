// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package netapp

import (
	_ "embed"

	"github.com/cobaltcore-dev/cortex/internal/db"
	"github.com/cobaltcore-dev/cortex/internal/extractor/plugins"
	"github.com/cobaltcore-dev/cortex/internal/sync/openstack/manila"
	"github.com/cobaltcore-dev/cortex/internal/sync/prometheus"
)

// Feature that maps the cpu usage of a storage pool.
type StoragePoolCPUUsage struct {
	// Name of the OpenStack storage pool.
	StoragePoolName string `db:"storage_pool_name"`
	// Avg CPU usage in pct.
	AvgCPUUsagePct float64 `db:"avg_cpu_usage_pct"`
	// Max CPU usage in pct.
	MaxCPUUsagePct float64 `db:"max_cpu_usage_pct"`
}

// Table under which the feature is stored.
func (StoragePoolCPUUsage) TableName() string {
	return "feature_storage_pool_cpu_usage"
}

// Indexes for the feature.
func (StoragePoolCPUUsage) Indexes() []db.Index {
	return []db.Index{
		{
			Name:        "idx_storage_pool_utilization_storage_pool_name",
			ColumnNames: []string{"storage_pool_name"},
		},
	}
}

// Extractor that extracts the CPU usage of a storage pool.
type StoragePoolCPUUsageExtractor struct {
	// Common base for all extractors that provides standard functionality.
	plugins.BaseExtractor[
		struct{},            // No options passed through yaml config
		StoragePoolCPUUsage, // Feature model
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
