// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package sap

type HostDetails struct {
	// Name of the OpenStack compute host.
	ComputeHost string `db:"compute_host"`
	// Availability zone of the compute host.
	AvailabilityZone string `db:"availability_zone"`
	// CPU Architecture of the compute host.
	// Can be "cascade-lake" or "sapphire-rapids"
	CPUArchitecture string `db:"cpu_architecture"`
	// Hypervisor type of the compute host.
	HypervisorType string `db:"hypervisor_type"`
	// Hypervisor family of the compute host.
	// Can be "kvm" or "vmware"
	HypervisorFamily string `db:"hypervisor_family"`
	// Amount of VMs currently running on the compute host.
	RunningVMs int `db:"running_vms"`
	// Type of workload running on the compute host.
	// Can be "general-purpose" or "hana"
	WorkloadType string `db:"workload_type"`
	// Whether the compute host can be used for workloads.
	Enabled bool `db:"enabled"`
	// Reason why the compute host is disabled, if applicable.
	DisabledReason *string `db:"disabled_reason"`
	// Comma separated list of pinned projects of the ComputeHost.
	PinnedProjects *string `db:"pinned_projects"`
}

// Table under which the feature is stored.
func (HostDetails) TableName() string {
	return "feature_sap_host_details_v2"
}

// Indexes for the feature.
func (HostDetails) Indexes() map[string][]string {
	return map[string][]string{
		"idx_host_details_compute_host": {"compute_host"},
	}
}
