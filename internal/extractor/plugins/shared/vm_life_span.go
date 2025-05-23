// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package shared

import (
	_ "embed"

	"github.com/cobaltcore-dev/cortex/internal/db"
	"github.com/cobaltcore-dev/cortex/internal/extractor/plugins"
	"github.com/cobaltcore-dev/cortex/internal/sync/openstack/nova"
)

// Feature that describes how long a vm existed before it was deleted.
type VMLifeSpan struct {
	// Time the vm stayed on the host in seconds.
	Duration int `db:"duration"`
	// Flavor id of the virtual machine.
	FlavorID string `db:"flavor_id"`
	// Flavor name of the virtual machine.
	FlavorName string `db:"flavor_name"`
	// The UUID of the virtual machine.
	InstanceUUID string `db:"instance_uuid"`
}

// Table under which the feature is stored.
func (VMLifeSpan) TableName() string {
	return "feature_vm_life_span"
}

// Indexes for the feature.
func (VMLifeSpan) Indexes() []db.Index {
	return nil
}

// Extractor that extracts the time elapsed until the vm was deleted.
type VMLifeSpanExtractor struct {
	// Common base for all extractors that provides standard functionality.
	plugins.BaseExtractor[
		struct{},   // No options passed through yaml config
		VMLifeSpan, // Feature model
	]
}

// Name of this feature extractor that is used in the yaml config, for logging etc.
func (*VMLifeSpanExtractor) GetName() string {
	return "vm_life_span_extractor"
}

// Get message topics that trigger a re-execution of this extractor.
func (VMLifeSpanExtractor) Triggers() []string {
	return []string{
		nova.TriggerNovaServersSynced,
		nova.TriggerNovaFlavorsSynced,
	}
}

//go:embed vm_life_span.sql
var vmLifeSpanQuery string

// Extract the time elapsed until the first migration of a virtual machine.
// Depends on the OpenStack servers and migrations to be synced.
func (e *VMLifeSpanExtractor) Extract() ([]plugins.Feature, error) {
	return e.ExtractSQL(vmLifeSpanQuery)
}
