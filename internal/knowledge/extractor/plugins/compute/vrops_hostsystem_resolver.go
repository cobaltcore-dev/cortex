// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package compute

// Feature that maps a vROps hostsystem to a nova compute host.
type ResolvedVROpsHostsystem struct {
	VROpsHostsystem string `db:"vrops_hostsystem"`
	NovaComputeHost string `db:"nova_compute_host"`
}
