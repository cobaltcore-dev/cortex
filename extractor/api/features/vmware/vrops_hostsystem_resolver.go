// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package vmware

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

// Indexes for the feature.
func (ResolvedVROpsHostsystem) Indexes() map[string][]string {
	return map[string][]string{
		"idx_vrops_resolved_hostsystem": {"vrops_hostsystem"},
	}

}
