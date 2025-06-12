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
type StoragePoolSpace struct {
	// Name of the OpenStack storage pool.
	StoragePoolName string `db:"storage_pool_name"`
	// Capacity left in GB. Note the data type here is float64.
	CapacityLeftGB float64 `db:"capacity_left_gb"`
	// Capacity left in pct.
	CapacityLeftPct float64 `db:"capacity_left_pct"`
}

// Table under which the feature is stored.
func (StoragePoolSpace) TableName() string {
	return "feature_storage_pool_space"
}

// Indexes for the feature.
func (StoragePoolSpace) Indexes() []db.Index {
	return []db.Index{
		{
			Name:        "idx_storage_pool_space_storage_pool_name",
			ColumnNames: []string{"storage_pool_name"},
		},
	}
}

// Extractor that extracts the space left on a storage pool.
type StoragePoolSpaceExtractor struct {
	// Common base for all extractors that provides standard functionality.
	plugins.BaseExtractor[
		struct{},         // No options passed through yaml config
		StoragePoolSpace, // Feature model
	]
}

// Name of this feature extractor that is used in the yaml config, for logging etc.
func (*StoragePoolSpaceExtractor) GetName() string {
	return "storage_pool_space_extractor"
}

// Get message topics that trigger a re-execution of this extractor.
func (StoragePoolSpaceExtractor) Triggers() []string {
	return []string{
		manila.TriggerManilaStoragePoolsSynced,
	}
}

//go:embed storage_pool_space.sql
var storagePoolSpaceQuery string

// Extract the space left on a storage pool.
func (e *StoragePoolSpaceExtractor) Extract() ([]plugins.Feature, error) {
	return e.ExtractSQL(storagePoolSpaceQuery)
}
