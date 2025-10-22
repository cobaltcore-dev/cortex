// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package shared

import (
	_ "embed"

	"github.com/cobaltcore-dev/cortex/knowledge/api/features/shared"
	"github.com/cobaltcore-dev/cortex/knowledge/internal/extractor/plugins"
)

// Extractor that extracts the time elapsed until the first migration of a virtual machine.
type VMHostResidencyExtractor struct {
	// Common base for all extractors that provides standard functionality.
	plugins.BaseExtractor[
		struct{},               // No options passed through yaml config
		shared.VMHostResidency, // Feature model
	]
}

//go:embed vm_host_residency.sql
var vmHostResidencyQuery string

// Extract the time elapsed until the first migration of a virtual machine.
// Depends on the OpenStack servers and migrations to be synced.
func (e *VMHostResidencyExtractor) Extract() ([]plugins.Feature, error) {
	return e.ExtractSQL(vmHostResidencyQuery)
}
