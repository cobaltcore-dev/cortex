// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package compute

// Feature that maps the traits of a compute host in OpenStack.
type HostCapabilities struct {
	// Name of the OpenStack compute host.
	ComputeHost string `db:"compute_host"`
	// Comma-separated list of traits assigned to the compute host.
	Traits string `db:"traits"`
	// The type of hypervisor running on the compute host.
	HypervisorType string `db:"hypervisor_type"`
}
