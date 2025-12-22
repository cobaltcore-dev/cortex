// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package compute

import (
	_ "embed"

	"github.com/cobaltcore-dev/cortex/internal/knowledge/extractor/plugins"
)

type HostAZ struct {
	// Name of the OpenStack compute host.
	ComputeHost string `db:"compute_host"`
	// Availability zone of the compute host, if available.
	AvailabilityZone *string `db:"availability_zone"`
}

type HostAZExtractor struct {
	// Common base for all extractors that provides standard functionality.
	plugins.BaseExtractor[
		struct{}, // No options passed through yaml config
		HostAZ,   // Feature model
	]
}

//go:embed host_az.sql
var hostAZQuery string

// Extract the traits of a compute host from the database.
func (e *HostAZExtractor) Extract() ([]plugins.Feature, error) {
	return e.ExtractSQL(hostAZQuery)
}
