// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package shared

import (
	_ "embed"

	"github.com/cobaltcore-dev/cortex/knowledge/api/features/shared"
	"github.com/cobaltcore-dev/cortex/knowledge/internal/extractor/plugins"
)

// Extractor that extracts the utilization on a compute host.
type HostUtilizationExtractor struct {
	// Common base for all extractors that provides standard functionality.
	plugins.BaseExtractor[
		struct{},               // No options passed through yaml config
		shared.HostUtilization, // Feature model
	]
}

//go:embed host_utilization.sql
var hostUtilizationQuery string

// Extract the utilization on a compute host.
// Depends on the OpenStack hypervisors to be synced.
func (e *HostUtilizationExtractor) Extract() ([]plugins.Feature, error) {
	return e.ExtractSQL(hostUtilizationQuery)
}
