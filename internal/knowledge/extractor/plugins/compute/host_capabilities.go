// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package compute

import (
	_ "embed"

	"github.com/cobaltcore-dev/cortex/internal/knowledge/extractor/plugins"
)

// Feature that maps the traits of a compute host in OpenStack.
type HostCapabilities struct {
	// Name of the OpenStack compute host.
	ComputeHost string `db:"compute_host"`
	// Comma-separated list of traits assigned to the compute host.
	Traits string `db:"traits"`
	// The type of hypervisor running on the compute host.
	HypervisorType string `db:"hypervisor_type"`
}

// Extractor that extracts the traits of a compute host.
type HostCapabilitiesExtractor struct {
	// Common base for all extractors that provides standard functionality.
	plugins.BaseExtractor[
		struct{},         // No options passed through yaml config
		HostCapabilities, // Feature model
	]
}

//go:embed host_capabilities.sql
var hostCapabilitiesQuery string

// Extract the traits of a compute host from the database.
func (e *HostCapabilitiesExtractor) Extract() ([]plugins.Feature, error) {
	return e.ExtractSQL(hostCapabilitiesQuery)
}
