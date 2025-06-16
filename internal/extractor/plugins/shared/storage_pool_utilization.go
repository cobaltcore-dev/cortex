// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package shared

import (
	_ "embed"

	"github.com/cobaltcore-dev/cortex/internal/db"
	"github.com/cobaltcore-dev/cortex/internal/extractor/plugins"
	"github.com/cobaltcore-dev/cortex/internal/sync/openstack/manila"
)

// Feature that maps the space left on a storage pool.
type StoragePoolUtilization struct {
	// Name of the OpenStack storage pool.
	StoragePoolName string `db:"storage_pool_name"`
	// Capacity utilization in pct.
	CapacityUtilizedPct float64 `db:"capacity_utilized_pct"`
}

// Table under which the feature is stored.
func (StoragePoolUtilization) TableName() string {
	return "feature_storage_pool_utilization"
}

// Indexes for the feature.
func (StoragePoolUtilization) Indexes() []db.Index {
	return []db.Index{
		{
			Name:        "idx_storage_pool_utilization_storage_pool_name",
			ColumnNames: []string{"storage_pool_name"},
		},
	}
}

// Extractor that extracts the space left on a storage pool.
type StoragePoolUtilizationExtractor struct {
	// Common base for all extractors that provides standard functionality.
	plugins.BaseExtractor[
		struct{},               // No options passed through yaml config
		StoragePoolUtilization, // Feature model
	]
}

// Name of this feature extractor that is used in the yaml config, for logging etc.
func (*StoragePoolUtilizationExtractor) GetName() string {
	return "storage_pool_utilization_extractor"
}

// Get message topics that trigger a re-execution of this extractor.
func (StoragePoolUtilizationExtractor) Triggers() []string {
	return []string{
		manila.TriggerManilaStoragePoolsSynced,
	}
}

//go:embed storage_pool_utilization.sql
var storagePoolUtilizationQuery string

// Extract the space left on a storage pool.
func (e *StoragePoolUtilizationExtractor) Extract() ([]plugins.Feature, error) {
	return e.ExtractSQL(storagePoolUtilizationQuery)
}
