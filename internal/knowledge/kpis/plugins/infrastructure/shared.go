// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package infrastructure

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"github.com/cobaltcore-dev/cortex/internal/knowledge/extractor/plugins/compute"
	"github.com/cobaltcore-dev/cortex/pkg/knowledge/datasources/openstack/identity"
	"github.com/cobaltcore-dev/cortex/pkg/knowledge/datasources/openstack/nova"
	hv1 "github.com/cobaltcore-dev/openstack-hypervisor-operator/api/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

const (
	vmwareHostDetailsKnowledgeName = "vmware-host-details"
	hostUtilizationKnowledgeName   = "host-utilization"
)

// Whether the compute host is a VMware host, following the SAP Cloud
// Infrastructure naming convention "nova-compute-*" (excluding ironic hosts).
func isVMwareComputeHost(host string) bool {
	return strings.HasPrefix(host, "nova-compute-") && !strings.HasPrefix(host, "nova-compute-ironic-")
}

// Whether the compute host is a KVM host, following the SAP Cloud
// Infrastructure naming convention "node*-bb*".
func isKVMComputeHost(host string) bool {
	rest, ok := strings.CutPrefix(host, "node")
	return ok && strings.Contains(rest, "-bb")
}

// Whether the server is active, i.e. neither deleted nor in error state.
func isActiveServer(s nova.Server) bool {
	return s.Status != "DELETED" && s.Status != "ERROR"
}

// Project name and domain of a project, empty if unknown.
type projectDetails struct {
	ProjectName string
	DomainID    string
	DomainName  string
}

// Look up the name and domain of all projects by their id.
func projectDetailsByID(projects []identity.Project, domains []identity.Domain) map[string]projectDetails {
	domainNames := make(map[string]string, len(domains))
	for _, d := range domains {
		domainNames[d.ID] = d.Name
	}
	details := make(map[string]projectDetails, len(projects))
	for _, p := range projects {
		details[p.ID] = projectDetails{ProjectName: p.Name, DomainID: p.DomainID, DomainName: domainNames[p.DomainID]}
	}
	return details
}

// Number of active instances per project, compute host, availability zone and flavor.
type projectInstanceCount struct {
	ProjectID        string
	ProjectName      string
	DomainID         string
	DomainName       string
	ComputeHost      string
	FlavorName       string
	AvailabilityZone string
	InstanceCount    float64
}

// Capacity used by active instances per project, compute host and availability zone.
type projectCapacityUsage struct {
	ProjectID        string
	ProjectName      string
	DomainID         string
	DomainName       string
	ComputeHost      string
	AvailabilityZone string
	TotalVCPUs       float64
	TotalRAMMB       float64
	TotalDiskGB      float64
}

// Count the active instances on matching compute hosts per project, compute
// host, availability zone and flavor.
func projectInstanceCounts(
	servers []nova.Server,
	projects map[string]projectDetails,
	isComputeHost func(string) bool,
) []projectInstanceCount {

	type key struct{ projectID, computeHost, az, flavorName string }
	counts := make(map[key]float64)
	for _, s := range servers {
		if !isActiveServer(s) || !isComputeHost(s.OSEXTSRVATTRHost) {
			continue
		}
		counts[key{s.TenantID, s.OSEXTSRVATTRHost, s.OSEXTAvailabilityZone, s.FlavorName}]++
	}
	rows := make([]projectInstanceCount, 0, len(counts))
	for k, n := range counts {
		p := projects[k.projectID]
		rows = append(rows, projectInstanceCount{
			ProjectID:        k.projectID,
			ProjectName:      p.ProjectName,
			DomainID:         p.DomainID,
			DomainName:       p.DomainName,
			ComputeHost:      k.computeHost,
			FlavorName:       k.flavorName,
			AvailabilityZone: k.az,
			InstanceCount:    n,
		})
	}
	return rows
}

// Sum up the flavor resources of the active instances on matching compute
// hosts per project, compute host and availability zone. Instances with
// unknown flavors are counted without resources.
func projectCapacityUsages(
	servers []nova.Server,
	flavors []nova.Flavor,
	projects map[string]projectDetails,
	isComputeHost func(string) bool,
) []projectCapacityUsage {

	flavorsByName := make(map[string]nova.Flavor, len(flavors))
	for _, f := range flavors {
		flavorsByName[f.Name] = f
	}
	type key struct{ projectID, computeHost, az string }
	usages := make(map[key]*projectCapacityUsage)
	for _, s := range servers {
		if !isActiveServer(s) || !isComputeHost(s.OSEXTSRVATTRHost) {
			continue
		}
		k := key{s.TenantID, s.OSEXTSRVATTRHost, s.OSEXTAvailabilityZone}
		usage, ok := usages[k]
		if !ok {
			p := projects[k.projectID]
			usage = &projectCapacityUsage{
				ProjectID:        k.projectID,
				ProjectName:      p.ProjectName,
				DomainID:         p.DomainID,
				DomainName:       p.DomainName,
				ComputeHost:      k.computeHost,
				AvailabilityZone: k.az,
			}
			usages[k] = usage
		}
		if f, ok := flavorsByName[s.FlavorName]; ok {
			usage.TotalVCPUs += float64(f.VCPUs)
			usage.TotalRAMMB += float64(f.RAM)
			usage.TotalDiskGB += float64(f.Disk)
		}
	}
	rows := make([]projectCapacityUsage, 0, len(usages))
	for _, usage := range usages {
		rows = append(rows, *usage)
	}
	return rows
}

// vmwareHost wraps VMwareHostDetails with Prometheus metric helpers.
type vmwareHost struct {
	compute.VMwareHostDetails
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
	physicalHostSize := h.PhysicalHostSize
	if physicalHostSize == "" {
		physicalHostSize = "unknown"
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
		physicalHostSize,
		h.VCenter,
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
	"physical_host_size",
	"vcenter",
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
	"os_version",
	"status",
	"ready",
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

	osVersion := h.Status.OperatingSystem.Version
	if osVersion == "" {
		osVersion = "unknown"
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
	// status mirrors the "State" column shown by kubectl for a Hypervisor.
	status := "unknown"
	ready := false
	if cond := meta.FindStatusCondition(h.Status.Conditions, hv1.ConditionTypeReady); cond != nil {
		status = cond.Reason
		ready = cond.Status == metav1.ConditionTrue
	}

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
		osVersion,
		status,
		strconv.FormatBool(ready),
	}
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
