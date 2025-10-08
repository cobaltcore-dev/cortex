// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package shared

// Feature that maps the traits of a compute host in OpenStack.
type HostCapabilities struct {
	// Name of the OpenStack compute host.
	ComputeHost string `db:"compute_host"`
	// Comma-separated list of traits assigned to the compute host.
	Traits string `db:"traits"`
	// The type of hypervisor running on the compute host.
	HypervisorType string `db:"hypervisor_type"`
}

// Table under which the feature is stored.
func (HostCapabilities) TableName() string {
	return "feature_host_capabilities"
}

// Indexes for the feature.
func (HostCapabilities) Indexes() map[string][]string {
	return map[string][]string{
		"idx_host_capabilities_compute_host": {"compute_host"},
	}
}
