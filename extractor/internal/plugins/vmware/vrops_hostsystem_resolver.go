// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package vmware

import (
	_ "embed"

	"github.com/cobaltcore-dev/cortex/extractor/api/features/vmware"
	"github.com/cobaltcore-dev/cortex/extractor/internal/plugins"
	"github.com/cobaltcore-dev/cortex/internal/sync/openstack/nova"
	"github.com/cobaltcore-dev/cortex/internal/sync/prometheus"
)

// Extractor that resolves the vROps metrics hostsystem label to the
// corresponding Nova compute host and stores it as a feature into the database.
type VROpsHostsystemResolver struct {
	// Common base for all extractors that provides standard functionality.
	plugins.BaseExtractor[
		struct{},                       // No options passed through yaml config
		vmware.ResolvedVROpsHostsystem, // Feature model
	]
}

// Get message topics that trigger a re-execution of this extractor.
func (VROpsHostsystemResolver) Triggers() []string {
	return []string{
		nova.TriggerNovaServersSynced,
		nova.TriggerNovaHypervisorsSynced,
		prometheus.TriggerMetricTypeSynced("vrops_vm_metrics"),
	}
}

// Name of this feature extractor that is used in the yaml config, for logging etc.
func (e *VROpsHostsystemResolver) GetName() string {
	return "vrops_hostsystem_resolver"
}

//go:embed vrops_hostsystem_resolver.sql
var vropsHostsystemSQL string

// Resolve vROps hostsystems to Nova compute hosts.
func (e *VROpsHostsystemResolver) Extract() ([]plugins.Feature, error) {
	return e.ExtractSQL(vropsHostsystemSQL)
}
