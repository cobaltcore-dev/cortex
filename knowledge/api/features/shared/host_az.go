// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package shared

type HostAZ struct {
	// Name of the OpenStack compute host.
	ComputeHost string `db:"compute_host"`
	// Availability zone of the compute host, if available.
	AvailabilityZone *string `db:"availability_zone"`
}

// Table under which the feature is stored.
func (HostAZ) TableName() string {
	return "feature_host_az"
}

// Indexes for the feature.
func (HostAZ) Indexes() map[string][]string {
	return map[string][]string{
		"idx_host_az_compute_host": {"compute_host"},
	}
}
