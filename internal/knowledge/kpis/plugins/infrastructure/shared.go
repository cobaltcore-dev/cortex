// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package infrastructure

import (
	"fmt"
	"regexp"
	"strconv"

	"github.com/cobaltcore-dev/cortex/internal/knowledge/extractor/plugins/compute"
)

const (
	hostDetailsKnowledgeName       = "host-details"
	vmwareIronicHypervisorType     = "ironic"
	hypervisorFamilyVMware         = "vmware"
	vmwareComputeHostPattern       = "nova-compute-%"
	vmwareIronicComputeHostPattern = "nova-compute-ironic-%"
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
		h.HypervisorFamily,
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
	"hypervisor_family",
	"enabled",
	"decommissioned",
	"external_customer",
	"disabled_reason",
	"pinned_projects",
	"pinned_project_ids",
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
