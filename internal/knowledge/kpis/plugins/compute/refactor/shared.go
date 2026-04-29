// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package refactor

import (
	"fmt"
	"regexp"
	"strconv"

	"github.com/cobaltcore-dev/cortex/internal/knowledge/extractor/plugins/compute"
	"github.com/prometheus/client_golang/prometheus"
)

const (
	hypervisorTypeIronic   = "ironic"
	hypervisorFamilyVMware = "vmware"
	hypervisorFamilyKVM    = "kvm"

	cpuArchCascadeLake    = "cascade-lake"
	cpuArchSapphireRapids = "sapphire-rapids"

	workloadTypeHANA           = "hana"
	workloadTypeGeneralPurpose = "general-purpose"

	hostDetailsKnowledge     = "host-details"
	hostUtilizationKnowledge = "host-utilization"

	commitmentStatusConfirmed  = "confirmed"
	commitmentStatusGuaranteed = "guaranteed"

	limesServiceCompute = "compute"
	limesResourceCores  = "cores"
	limesResourceRAM    = "ram"

	unitVCPU  = "vCPU"
	unitBytes = "B"

	// Flavor suffix indicating sapphire-rapids CPU architecture.
	sapphireRapidsFlavorSuffix = "_v2"
)

// kvmFlavorRegex matches KVM flavors where the second underscore-delimited segment is "k", e.g. "m1_k_small".
var kvmFlavorRegex = regexp.MustCompile(`^[^_]+_k_`)

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

var vmwareHostCapacityLabels = append(append([]string{}, vmwareHostLabels...), "resource", "unit")

var vmwareProjectLabels = append(append([]string{}, vmwareHostLabels...), "project_id", "project_name", "flavor_name")

var vmwareProjectCapacityLabels = append(append([]string{}, vmwareProjectLabels...), "resource", "unit")

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

var kvmHostCapacityLabels = append(append([]string{}, kvmHostLabels...), "resource", "unit")

var kvmProjectLabels = append(append([]string{}, kvmHostLabels...), "project_id", "project_name", "flavor_name")

var kvmProjectCapacityLabels = append(append([]string{}, kvmProjectLabels...), "resource", "unit")

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

func (h vmwareHost) toCapacityMetric(desc *prometheus.Desc, resource, unit string, value float64) prometheus.Metric {
	labels := append(h.getHostLabels(), resource, unit)
	return prometheus.MustNewConstMetric(desc, prometheus.GaugeValue, value, labels...)
}

func (h vmwareHost) toInstanceCountMetric(desc *prometheus.Desc, value float64) prometheus.Metric {
	return prometheus.MustNewConstMetric(desc, prometheus.GaugeValue, value, h.getHostLabels()...)
}

// convertLimesMemoryToBytes converts a limes memory amount and its unit string to bytes.
func convertLimesMemoryToBytes(amount uint64, unit string) (float64, error) {
	switch unit {
	case "B", "":
		return float64(amount), nil
	case "KiB":
		return float64(amount) * 1024, nil
	case "MiB":
		return float64(amount) * 1024 * 1024, nil
	case "GiB":
		return float64(amount) * 1024 * 1024 * 1024, nil
	case "TiB":
		return float64(amount) * 1024 * 1024 * 1024 * 1024, nil
	default:
		return 0, fmt.Errorf("unknown limes memory unit: %s", unit)
	}
}

// cpuArchForFlavor derives the CPU architecture from a flavor name.
// Flavors with a "_v2" suffix run on sapphire-rapids; all others on cascade-lake.
func cpuArchForFlavor(flavorName string) string {
	if len(flavorName) >= len(sapphireRapidsFlavorSuffix) &&
		flavorName[len(flavorName)-len(sapphireRapidsFlavorSuffix):] == sapphireRapidsFlavorSuffix {
		return cpuArchSapphireRapids
	}
	return cpuArchCascadeLake
}
