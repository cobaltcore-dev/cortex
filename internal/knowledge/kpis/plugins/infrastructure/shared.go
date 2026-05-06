// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package infrastructure

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"github.com/cobaltcore-dev/cortex/internal/knowledge/extractor/plugins/compute"
	hv1 "github.com/cobaltcore-dev/openstack-hypervisor-operator/api/v1"
	"k8s.io/apimachinery/pkg/api/resource"
)

const (
	hostDetailsKnowledgeName       = "host-details"
	hostUtilizationKnowledgeName   = "host-utilization"
	vmwareIronicHypervisorType     = "ironic"
	hypervisorFamilyVMware         = "vmware"
	vmwareComputeHostPattern       = "nova-compute-%"
	vmwareIronicComputeHostPattern = "nova-compute-ironic-%"
	kvmComputeHostPattern          = "node%-bb%"
)

// vmwareHost wraps HostDetails with Prometheus metric helpers.
type vmwareHost struct {
	compute.HostDetails
}

func (h vmwareHost) getHostLabels() []string {
	pinnedProjectIds := ""
	pinnedProjects := false
	if h.PinnedProjects != nil {
		pinnedProjectIds = *h.PinnedProjects
		pinnedProjects = true
	}
	disabledReason := "-"
	if h.DisabledReason != nil {
		disabledReason = *h.DisabledReason
	}
	return []string{
		h.AvailabilityZone,
		h.ComputeHost,
		h.CPUArchitecture,
		h.WorkloadType,
		strconv.FormatBool(h.Enabled),
		strconv.FormatBool(h.Decommissioned),
		strconv.FormatBool(h.ExternalCustomer),
		disabledReason,
		strconv.FormatBool(pinnedProjects),
		pinnedProjectIds,
	}
}

var vmwareHostLabels = []string{
	"availability_zone",
	"compute_host",
	"cpu_architecture",
	"workload_type",
	"enabled",
	"decommissioned",
	"external_customer",
	"disabled_reason",
	"pinned_projects",
	"pinned_project_ids",
}

var kvmHostLabels = []string{
	"compute_host",
	"availability_zone",
	"building_block",
	"cpu_architecture",
	"workload_type",
	"enabled",
	"decommissioned",
	"external_customer",
	"maintenance",
}

type kvmHost struct {
	hv1.Hypervisor
}

func (h kvmHost) getHostLabels() []string {
	decommissioned := false
	externalCustomer := false
	workloadType := "general-purpose"
	cpuArchitecture := "cascade-lake"

	availabilityZone := h.Labels["topology.kubernetes.io/zone"]
	if availabilityZone == "" {
		availabilityZone = "unknown"
	}

	buildingBlock := "unknown"
	// Assuming hypervisor names are in the format nodeXXX-bbYY
	parts := strings.Split(h.Name, "-")
	if len(parts) > 1 {
		buildingBlock = parts[1]
	}

	for _, trait := range h.Status.Traits {
		switch trait {
		case "CUSTOM_HW_SAPPHIRE_RAPIDS":
			cpuArchitecture = "sapphire-rapids"
		case "CUSTOM_HANA_EXCLUSIVE_HOST":
			workloadType = "hana"
		case "CUSTOM_DECOMMISSIONING":
			decommissioned = true
		case "CUSTOM_EXTERNAL_CUSTOMER_EXCLUSIVE":
			externalCustomer = true
		}
	}

	maintenance := h.Spec.Maintenance != hv1.MaintenanceUnset

	return []string{
		h.Name,
		availabilityZone,
		buildingBlock,
		cpuArchitecture,
		workloadType,
		strconv.FormatBool(true),
		strconv.FormatBool(decommissioned),
		strconv.FormatBool(externalCustomer),
		strconv.FormatBool(maintenance),
	}
}

// getResourceCapacity attempts to retrieve the effective capacity for the specified resource from the hypervisor status, falling back to the physical capacity if effective capacity is not available. It returns the capacity quantity and a boolean indicating whether any capacity information was found.
func (k kvmHost) getResourceCapacity(resourceName hv1.ResourceName) (capacity resource.Quantity, ok bool) {
	if k.Status.EffectiveCapacity != nil {
		qty, exists := k.Status.EffectiveCapacity[resourceName]
		if exists && !qty.IsZero() {
			return qty, true
		}
	}
	if k.Status.Capacity == nil {
		return resource.Quantity{}, false
	}
	qty, exists := k.Status.Capacity[resourceName]
	if !exists || qty.IsZero() {
		return resource.Quantity{}, false
	}
	return qty, true
}

func (k kvmHost) getResourceAllocation(resourceName hv1.ResourceName) (allocation resource.Quantity) {
	if k.Status.Allocation == nil {
		return resource.MustParse("0")
	}

	qty, exists := k.Status.Allocation[resourceName]
	if !exists {
		return resource.MustParse("0")
	}
	return qty
}

var fqNameRe = regexp.MustCompile(`fqName: "([^"]+)"`)

func getMetricName(desc string) string {
	match := fqNameRe.FindStringSubmatch(desc)
	if len(match) > 1 {
		return match[1]
	}
	return ""
}

type collectedVMwareMetric struct {
	Name   string
	Labels map[string]string
	Value  float64
}

// kvmFlavorPattern matches KVM flavors where the second underscore-delimited
// segment is "k" (e.g. "m1_k_small", "hana_k_large").
var kvmFlavorPattern = regexp.MustCompile(`^[^_]+_k_`)

// isKVMFlavor reports whether flavorName belongs to a KVM hypervisor.
func isKVMFlavor(name string) bool {
	return kvmFlavorPattern.MatchString(name)
}

// cpuArchitectureRule maps a flavor name regex to a CPU architecture label.
type cpuArchitectureRule struct {
	pattern *regexp.Regexp
	arch    string
}

// flavorCPUArchitectureRules maps flavor name patterns to CPU architecture labels in priority order.
// The first matching rule wins; defaultCPUArch is used when none match.
var flavorCPUArchitectureRules = []cpuArchitectureRule{
	{regexp.MustCompile(`_v2$`), "sapphire-rapids"},
}

const defaultCPUArchitecture = "cascade-lake"

// flavorCPUArchitecture derives the CPU architecture label from a flavor name.
func flavorCPUArchitecture(flavorName string) string {
	for _, rule := range flavorCPUArchitectureRules {
		if rule.pattern.MatchString(flavorName) {
			return rule.arch
		}
	}
	return defaultCPUArchitecture
}

// bytesPerUnit maps memory unit strings to their byte multipliers.
var bytesPerUnit = map[string]float64{
	"":    1,
	"B":   1,
	"KiB": 1024,
	"MB":  1024 * 1024,
	"MiB": 1024 * 1024,
	"GB":  1024 * 1024 * 1024,
	"GiB": 1024 * 1024 * 1024,
	"TiB": 1024 * 1024 * 1024 * 1024,
}

// bytesFromUnit converts an amount in the given unit to bytes.
func bytesFromUnit(amount float64, unit string) (float64, error) {
	multiplier, ok := bytesPerUnit[unit]
	if !ok {
		return 0, fmt.Errorf("unknown memory unit: %s", unit)
	}
	return amount * multiplier, nil
}
