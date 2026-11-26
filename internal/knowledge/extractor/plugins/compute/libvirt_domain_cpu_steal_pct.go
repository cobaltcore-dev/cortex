// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package compute

// Feature that maps the maximum cpu steal time of a virtual machine.
type LibvirtDomainCPUStealPct struct {
	// The openstack server instance uuid.
	InstanceUUID string `db:"instance_uuid"`
	// The compute host on which the instance is running.
	Host string `db:"host"`
	// The maximum steal pct recorded.
	MaxStealTimePct float64 `db:"max_steal_time_pct"`
}
