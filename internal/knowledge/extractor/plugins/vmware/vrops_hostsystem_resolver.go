// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package vmware

import (
	_ "embed"

	"github.com/cobaltcore-dev/cortex/internal/knowledge/extractor/plugins"
)

// Feature that resolves the vROps metrics hostsystem label to the
// corresponding Nova compute host.
type ResolvedVROpsHostsystem struct {
	VROpsHostsystem string `db:"vrops_hostsystem"`
	NovaComputeHost string `db:"nova_compute_host"`
}

// Extractor that resolves the vROps metrics hostsystem label to the
// corresponding Nova compute host and stores it as a feature into the database.
type VROpsHostsystemResolver struct {
	// Common base for all extractors that provides standard functionality.
	plugins.BaseExtractor[
		struct{},                // No options passed through yaml config
		ResolvedVROpsHostsystem, // Feature model
	]
}

//go:embed vrops_hostsystem_resolver.sql
var vropsHostsystemSQL string

// Resolve vROps hostsystems to Nova compute hosts.
func (e *VROpsHostsystemResolver) Extract() ([]plugins.Feature, error) {
	return e.ExtractSQL(vropsHostsystemSQL)
}
