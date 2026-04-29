// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package infrastructure

import (
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
