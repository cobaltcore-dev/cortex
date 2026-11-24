// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package compute

import (
	"context"
	"slices"
	"strings"

	"github.com/cobaltcore-dev/cortex/pkg/knowledge/datasources/openstack/nova"
	"github.com/cobaltcore-dev/cortex/pkg/knowledge/datasources/openstack/placement"
	"github.com/cobaltcore-dev/cortex/pkg/knowledge/extractor"
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

// Inputs of the host utilization extractor.
type HostUtilizationInputs struct {
	Hypervisors     []nova.Hypervisor          `input:"hypervisors"`
	InventoryUsages []placement.InventoryUsage `input:"inventoryUsages"`
}

// Extractor that extracts the utilization on a compute host.
type HostUtilizationExtractor struct {
	// Common base for all extractors that provides standard functionality.
	extractor.BaseExtractor[struct{}]
}

// Extract the utilization of each hypervisor that has a placement inventory
// for MEMORY_MB, VCPU and DISK_GB.
//
// Resource usage formulas:
//   - "TotalAllocatableCapacity": The maximum usable resource after reserving
//     capacity and applying overcommit:
//     (placement.total - placement.reserved) * placement.allocation_ratio
//   - "UsedAbsolute": The actual amount of resource currently in use
//     (includes overcommit): placement.used
//   - "UsedPct": Percentage of allocatable capacity that is currently used:
//     placement.used / TotalAllocatableCapacity
//
// Note: The "TotalAllocatableCapacity" does not include the capacity reserved
// for failover capacity!
// Reference: https://github.com/openstack/placement/blob/4d3df47ee3e394e3178d58c15306620809ad2806/placement/objects/allocation.py#L224-L227
func (e *HostUtilizationExtractor) Extract(_ context.Context, in HostUtilizationInputs) ([]HostUtilization, error) {
	type inventories struct{ memory, vcpu, disk *placement.InventoryUsage }
	byProvider := make(map[string]*inventories)
	for i := range in.InventoryUsages {
		usage := &in.InventoryUsages[i]
		inv, ok := byProvider[usage.ResourceProviderUUID]
		if !ok {
			inv = &inventories{}
			byProvider[usage.ResourceProviderUUID] = inv
		}
		switch usage.InventoryClassName {
		case "MEMORY_MB":
			inv.memory = usage
		case "VCPU":
			inv.vcpu = usage
		case "DISK_GB":
			inv.disk = usage
		}
	}
	allocatable := func(u *placement.InventoryUsage) float64 {
		return float64(u.Total-u.Reserved) * float64(u.AllocationRatio)
	}
	pct := func(used, total float64) float64 {
		if total == 0 {
			return 0
		}
		return used / total * 100
	}
	var features []HostUtilization
	for _, h := range in.Hypervisors {
		inv, ok := byProvider[h.ID]
		if !ok || inv.memory == nil || inv.vcpu == nil || inv.disk == nil {
			continue
		}
		f := HostUtilization{
			ComputeHost:            h.ServiceHost,
			VCPUsUsed:              float64(inv.vcpu.Used),
			TotalVCPUsAllocatable:  allocatable(inv.vcpu),
			RAMUsedMB:              float64(inv.memory.Used),
			TotalRAMAllocatableMB:  allocatable(inv.memory),
			DiskUsedGB:             float64(inv.disk.Used),
			TotalDiskAllocatableGB: allocatable(inv.disk),
		}
		f.VCPUsUtilizedPct = pct(f.VCPUsUsed, f.TotalVCPUsAllocatable)
		f.RAMUtilizedPct = pct(f.RAMUsedMB, f.TotalRAMAllocatableMB)
		f.DiskUtilizedPct = pct(f.DiskUsedGB, f.TotalDiskAllocatableGB)
		features = append(features, f)
	}
	slices.SortStableFunc(features, func(a, b HostUtilization) int {
		return strings.Compare(a.ComputeHost, b.ComputeHost)
	})
	return features, nil
}

func init() {
	extractor.Register("host_utilization_extractor", func() extractor.Extractor[HostUtilizationInputs, HostUtilization] {
		return &HostUtilizationExtractor{}
	})
}
