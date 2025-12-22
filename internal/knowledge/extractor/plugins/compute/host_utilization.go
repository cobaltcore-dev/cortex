// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package compute

import (
	_ "embed"

	"github.com/cobaltcore-dev/cortex/internal/knowledge/extractor/plugins"
)

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

// Extractor that extracts the utilization on a compute host.
type HostUtilizationExtractor struct {
	// Common base for all extractors that provides standard functionality.
	plugins.BaseExtractor[
		struct{},        // No options passed through yaml config
		HostUtilization, // Feature model
	]
}

//go:embed host_utilization.sql
var hostUtilizationQuery string

// Extract the utilization on a compute host.
// Depends on the OpenStack hypervisors to be synced.
func (e *HostUtilizationExtractor) Extract() ([]plugins.Feature, error) {
	return e.ExtractSQL(hostUtilizationQuery)
}
