// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package shared

import (
	_ "embed"

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

//go:embed host_az.sql
var hostAZQuery string

// Extract the traits of a compute host from the database.
func (e *HostAZExtractor) Extract() ([]plugins.Feature, error) {
	return e.ExtractSQL(hostAZQuery)
}
