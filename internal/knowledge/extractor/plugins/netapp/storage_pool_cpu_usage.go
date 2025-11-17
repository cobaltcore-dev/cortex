// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package netapp

import (
	_ "embed"

	"github.com/cobaltcore-dev/cortex/internal/knowledge/extractor/plugins"
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
func (StoragePoolCPUUsage) Indexes() map[string][]string {
	return map[string][]string{
		"idx_storage_pool_utilization_storage_pool_name": {"storage_pool_name"},
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

//go:embed storage_pool_cpu_usage.sql
var storagePoolCPUUsageQuery string

// Extract the CPU usage of a storage pool.
func (e *StoragePoolCPUUsageExtractor) Extract() ([]plugins.Feature, error) {
	return e.ExtractSQL(storagePoolCPUUsageQuery)
}
