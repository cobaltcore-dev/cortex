// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package vmware

import (
	"github.com/cobaltcore-dev/cortex/internal/features/plugins"
)

// Feature that resolves the vROps metrics hostsystem label to the
// corresponding Nova compute host.
type ResolvedVROpsHostsystem struct {
	VROpsHostsystem string `db:"vrops_hostsystem"`
	NovaComputeHost string `db:"nova_compute_host"`
}

// Table under which the feature is stored.
func (ResolvedVROpsHostsystem) TableName() string {
	return "feature_vrops_resolved_hostsystem"
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

// Name of this feature extractor that is used in the yaml config, for logging etc.
func (e *VROpsHostsystemResolver) GetName() string {
	return "vrops_hostsystem_resolver"
}

// Resolve vROps hostsystems to Nova compute hosts.
func (e *VROpsHostsystemResolver) Extract() ([]plugins.Feature, error) {
	var features []ResolvedVROpsHostsystem
	if _, err := e.DB.Select(&features, `
		SELECT
			m.hostsystem AS vrops_hostsystem,
			h.service_host AS nova_compute_host
		FROM vrops_vm_metrics m
		JOIN openstack_servers s ON m.instance_uuid = s.id
		JOIN openstack_hypervisors h ON s.os_ext_srv_attr_hypervisor_hostname = h.hostname
		GROUP BY m.hostsystem, h.service_host;
    `); err != nil {
		return nil, err
	}
	return e.Extracted(features)
}
