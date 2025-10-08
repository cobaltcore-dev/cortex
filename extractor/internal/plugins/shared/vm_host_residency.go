// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package shared

import (
	_ "embed"

	"github.com/cobaltcore-dev/cortex/extractor/api/features/shared"
	"github.com/cobaltcore-dev/cortex/extractor/internal/plugins"
	"github.com/cobaltcore-dev/cortex/internal/sync/openstack/nova"
)

// Extractor that extracts the time elapsed until the first migration of a virtual machine.
type VMHostResidencyExtractor struct {
	// Common base for all extractors that provides standard functionality.
	plugins.BaseExtractor[
		struct{},               // No options passed through yaml config
		shared.VMHostResidency, // Feature model
	]
}

// Name of this feature extractor that is used in the yaml config, for logging etc.
func (*VMHostResidencyExtractor) GetName() string {
	return "vm_host_residency_extractor"
}

// Get message topics that trigger a re-execution of this extractor.
func (VMHostResidencyExtractor) Triggers() []string {
	return []string{
		nova.TriggerNovaServersSynced,
		nova.TriggerNovaMigrationsSynced,
		nova.TriggerNovaFlavorsSynced,
	}
}

//go:embed vm_host_residency.sql
var vmHostResidencyQuery string

// Extract the time elapsed until the first migration of a virtual machine.
// Depends on the OpenStack servers and migrations to be synced.
func (e *VMHostResidencyExtractor) Extract() ([]plugins.Feature, error) {
	return e.ExtractSQL(vmHostResidencyQuery)
}
