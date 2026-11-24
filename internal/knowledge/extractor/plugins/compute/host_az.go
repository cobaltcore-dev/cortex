// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package compute

// Feature that maps the availability zone of a compute host.
type HostAZ struct {
	// Name of the OpenStack compute host.
	ComputeHost string `db:"compute_host"`
	// Availability zone of the compute host, if available.
	AvailabilityZone *string `db:"availability_zone"`
}
