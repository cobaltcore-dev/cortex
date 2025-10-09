// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package shared

// Feature that maps how many resources are utilized on a compute host.
type HostUtilization struct {
	// Name of the OpenStack compute host.
	ComputeHost string `db:"compute_host" json:"computeHost"`

	// VCPU resource usage
	VCPUsUsed             float64 `db:"vcpus_used" json:"vcpusUsed"`
	VCPUsUtilizedPct      float64 `db:"vcpus_utilized_pct" json:"vcpusUtilizedPct"`
	TotalVCPUsAllocatable float64 `db:"total_vcpus_allocatable" json:"totalVCPUsAllocatable"`

	// RAM resource usage
	RAMUsedMB             float64 `db:"ram_used_mb" json:"ramUsedMB"`
	RAMUtilizedPct        float64 `db:"ram_utilized_pct" json:"ramUtilizedPct"`
	TotalRAMAllocatableMB float64 `db:"total_ram_allocatable_mb" json:"totalRAMAllocatableMB"`

	// Disk resource usage
	DiskUsedGB             float64 `db:"disk_used_gb" json:"diskUsedGB"`
	DiskUtilizedPct        float64 `db:"disk_utilized_pct" json:"diskUtilizedPct"`
	TotalDiskAllocatableGB float64 `db:"total_disk_allocatable_gb" json:"totalDiskAllocatableGB"`
}

// Table under which the feature is stored.
func (HostUtilization) TableName() string {
	return "feature_host_utilization_v2"
}

// Indexes for the feature.
func (HostUtilization) Indexes() map[string][]string {
	return map[string][]string{
		"idx_host_utilization_compute_host": {"compute_host"},
	}
}
