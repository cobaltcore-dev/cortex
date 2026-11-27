// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package compute

import (
	"context"
	gomath "math"
	"slices"
	"strconv"
	"strings"

	"github.com/cobaltcore-dev/cortex/pkg/knowledge/datasources/openstack/nova"
	"github.com/cobaltcore-dev/cortex/pkg/knowledge/datasources/openstack/placement"
	"github.com/cobaltcore-dev/cortex/pkg/knowledge/extractor"
)

// VMwareHostDetails describes a single VMware compute host. Only VMware hosts
// are extracted here (KVM hosts are served by a dedicated CRD, and ironic hosts
// are excluded); the extractor filters everything else out.
type VMwareHostDetails struct {
	// Name of the OpenStack compute host.
	ComputeHost string `db:"compute_host"`
	// Availability zone of the compute host.
	AvailabilityZone string `db:"availability_zone"`
	// CPU Architecture of the compute host.
	// Can be "cascade-lake" or "sapphire-rapids"
	CPUArchitecture string `db:"cpu_architecture"`
	// Amount of VMs currently running on the compute host.
	RunningVMs int `db:"running_vms"`
	// Type of workload running on the compute host.
	// Can be "general-purpose" or "hana"
	WorkloadType string `db:"workload_type"`
	// Whether the compute host is decommissioned.
	Decommissioned bool `db:"decommissioned"`
	// Whether the compute host is reserved for external customers.
	ExternalCustomer bool `db:"external_customer"`
	// Whether the compute host can be used for workloads.
	Enabled bool `db:"enabled"`
	// Reason why the compute host is disabled, if applicable.
	DisabledReason *string `db:"disabled_reason"`
	// Comma separated list of pinned projects of the ComputeHost.
	PinnedProjects *string `db:"pinned_projects"`
	// Physical size category of a single host inside a VMware building block,
	// derived from the memory inventory and rounded to the nearest TiB
	// (e.g. "4TiB"), or to the nearest GiB (e.g. "512GiB") when smaller than
	// 1 TiB. "unknown" when the memory inventory is missing.
	PhysicalHostSize string `db:"physical_host_size"`
	// VCenter of the building block this host belongs to, derived from the
	// OpenStack aggregate whose name starts with "vc-". "unknown" when no such
	// aggregate is associated with the host.
	VCenter string `db:"vcenter"`
}

// Inputs of the vmware host details extractor.
type VMwareHostDetailsInputs struct {
	Hypervisors     []nova.Hypervisor          `input:"hypervisors"`
	Traits          []placement.Trait          `input:"traits"`
	InventoryUsages []placement.InventoryUsage `input:"inventoryUsages"`
	Aggregates      []nova.Aggregate           `input:"aggregates"`
	// Features of the host pinned projects knowledge.
	PinnedProjects []HostPinnedProjects `input:"pinnedProjects"`
	// Features of the host availability zone knowledge.
	HostAZs []HostAZ `input:"hostAZs"`
}

type VMwareHostDetailsExtractor struct {
	// Common base for all extractors that provides standard functionality.
	extractor.BaseExtractor[struct{}]
}

// Extract the details of the VMware compute hosts. Only hypervisors whose
// service host matches the VMware naming convention (nova-compute-*) are
// considered, while ironic ones (nova-compute-ironic-*) are dropped.
func (e *VMwareHostDetailsExtractor) Extract(_ context.Context, in VMwareHostDetailsInputs) ([]VMwareHostDetails, error) {
	traitsByProvider := make(map[string][]string)
	for _, t := range in.Traits {
		traitsByProvider[t.ResourceProviderUUID] = append(traitsByProvider[t.ResourceProviderUUID], t.Name)
	}
	memoryByProvider := make(map[string]placement.InventoryUsage)
	for _, i := range in.InventoryUsages {
		if i.InventoryClassName == "MEMORY_MB" {
			memoryByProvider[i.ResourceProviderUUID] = i
		}
	}
	// VCenter of the building block, derived from the aggregate whose name
	// starts with "vc-". If a host is member of multiple such aggregates,
	// the lowest name is used.
	vcenterByHost := make(map[string]string)
	for _, a := range in.Aggregates {
		if a.ComputeHost == nil || !strings.HasPrefix(a.Name, "vc-") {
			continue
		}
		if vc, ok := vcenterByHost[*a.ComputeHost]; !ok || a.Name < vc {
			vcenterByHost[*a.ComputeHost] = a.Name
		}
	}
	pinnedProjectsByHost := make(map[string][]string)
	for _, pp := range in.PinnedProjects {
		if pp.ComputeHost != nil && pp.Label != nil {
			pinnedProjectsByHost[*pp.ComputeHost] = append(pinnedProjectsByHost[*pp.ComputeHost], *pp.Label)
		}
	}
	azByHost := make(map[string]string)
	for _, hostAZ := range in.HostAZs {
		if hostAZ.AvailabilityZone != nil {
			azByHost[hostAZ.ComputeHost] = *hostAZ.AvailabilityZone
		}
	}

	var features []VMwareHostDetails
	for _, h := range in.Hypervisors {
		if !strings.HasPrefix(h.ServiceHost, "nova-compute-") ||
			strings.HasPrefix(h.ServiceHost, "nova-compute-ironic-") {
			continue
		}
		traits := traitsByProvider[h.ID]
		hasTrait := func(name string) bool { return slices.Contains(traits, name) }
		details := VMwareHostDetails{
			ComputeHost:      h.ServiceHost,
			AvailabilityZone: "unknown",
			CPUArchitecture:  "cascade-lake",
			RunningVMs:       h.RunningVMs,
			WorkloadType:     "general-purpose",
			Decommissioned:   hasTrait("CUSTOM_DECOMMISSIONING"),
			ExternalCustomer: hasTrait("CUSTOM_EXTERNAL_CUSTOMER_EXCLUSIVE"),
			Enabled:          true,
			PhysicalHostSize: "unknown",
			VCenter:          "unknown",
		}
		if hasTrait("CUSTOM_HW_SAPPHIRE_RAPIDS") {
			details.CPUArchitecture = "sapphire-rapids"
		}
		if hasTrait("CUSTOM_HANA_EXCLUSIVE_HOST") {
			details.WorkloadType = "hana"
		}
		reason := "--"
		if h.ServiceDisabledReason != nil {
			reason = *h.ServiceDisabledReason
		}
		var disabledReason string
		switch {
		case hasTrait("COMPUTE_STATUS_DISABLED"), h.Status != "enabled":
			disabledReason = "[disabled] " + reason
		case h.State != "up":
			disabledReason = "[down] " + reason
		}
		if disabledReason != "" {
			details.Enabled = false
			details.DisabledReason = &disabledReason
		}
		if memory, ok := memoryByProvider[h.ID]; ok {
			if size, ok := physicalHostSize(memory); ok {
				details.PhysicalHostSize = size
			}
		}
		if vc, ok := vcenterByHost[h.ServiceHost]; ok {
			details.VCenter = vc
		}
		if pps, ok := pinnedProjectsByHost[h.ServiceHost]; ok {
			joined := strings.Join(pps, ",")
			details.PinnedProjects = &joined
		}
		if az, ok := azByHost[h.ServiceHost]; ok {
			details.AvailabilityZone = az
		}
		features = append(features, details)
	}
	slices.SortStableFunc(features, func(a, b VMwareHostDetails) int {
		return strings.Compare(a.ComputeHost, b.ComputeHost)
	})
	return features, nil
}

// Physical host size category of a single host inside a VMware building block.
//
// A building block pools multiple physical hosts into one resource provider,
// so the per-host memory size is recovered from the pooled memory inventory:
//
//	amount_hosts        = (total - reserved) / max_unit
//	physical_host_size  = max_unit + reserved / amount_hosts   (in MiB)
//
// The size is first rounded to whole GiB; if that is at least 1024 GiB it is
// reported in TiB, otherwise in GiB. Rounding to GiB first avoids a value like
// 1023.6 GiB being shown as "1024GiB" instead of "1TiB". Values are rounded
// half to even.
func physicalHostSize(memory placement.InventoryUsage) (string, bool) {
	if memory.MaxUnit <= 0 || memory.Total-memory.Reserved <= 0 {
		return "", false
	}
	amountHosts := float64(memory.Total-memory.Reserved) / float64(memory.MaxUnit)
	sizeMB := float64(memory.MaxUnit) + float64(memory.Reserved)/amountHosts
	sizeGiB := gomath.RoundToEven(sizeMB / 1024)
	if sizeGiB >= 1024 {
		return strconv.Itoa(int(gomath.RoundToEven(sizeGiB/1024))) + "TiB", true
	}
	return strconv.Itoa(int(sizeGiB)) + "GiB", true
}

func init() {
	extractor.Register("vmware_host_details_extractor", func() extractor.Extractor[VMwareHostDetailsInputs, VMwareHostDetails] {
		return &VMwareHostDetailsExtractor{}
	})
}
