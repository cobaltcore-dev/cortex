// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package shared

import (
	_ "embed"

	"github.com/cobaltcore-dev/cortex/knowledge/api/datasources/openstack/nova"
	"github.com/cobaltcore-dev/cortex/knowledge/api/features/shared"
	"github.com/cobaltcore-dev/cortex/knowledge/internal/extractor/plugins"
)

type HostAZExtractor struct {
	// Common base for all extractors that provides standard functionality.
	plugins.BaseExtractor[
		struct{},      // No options passed through yaml config
		shared.HostAZ, // Feature model
	]
}

// Name of this feature extractor that is used in the yaml config, for logging etc.
func (*HostAZExtractor) GetName() string {
	return "host_az_extractor"
}

// Get message topics that trigger a re-execution of this extractor.
func (HostAZExtractor) Triggers() []string {
	return []string{
		nova.TriggerNovaAggregatesSynced,
		nova.TriggerNovaHypervisorsSynced,
	}
}

//go:embed host_az.sql
var hostAZQuery string

// Extract the traits of a compute host from the database.
func (e *HostAZExtractor) Extract() ([]plugins.Feature, error) {
	return e.ExtractSQL(hostAZQuery)
}
