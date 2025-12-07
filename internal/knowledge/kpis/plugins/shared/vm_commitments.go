// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package shared

import (
	"fmt"
	"log/slog"
	"strings"

	"github.com/cobaltcore-dev/cortex/internal/knowledge/datasources/openstack/limes"
	"github.com/cobaltcore-dev/cortex/internal/knowledge/datasources/openstack/nova"
	"github.com/cobaltcore-dev/cortex/internal/knowledge/kpis/plugins"
	"github.com/cobaltcore-dev/cortex/pkg/conf"
	"github.com/cobaltcore-dev/cortex/pkg/db"
	"github.com/prometheus/client_golang/prometheus"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

type VMCommitmentsKPI struct {
	// Common base for all KPIs that provides standard functionality.
	plugins.BaseKPI[struct{}] // No options passed through yaml config

	vmCommitmentsTotalDesc *prometheus.Desc
	vmCommitmentsSumDesc   *prometheus.Desc
	committedCoresDesc     *prometheus.Desc
	committedMemoryDesc    *prometheus.Desc
}

func (VMCommitmentsKPI) GetName() string {
	return "vm_commitments_kpi"
}

func (k *VMCommitmentsKPI) Init(db *db.DB, client client.Client, opts conf.RawOpts) error {
	if err := k.BaseKPI.Init(db, client, opts); err != nil {
		return err
	}
	k.vmCommitmentsTotalDesc = prometheus.NewDesc(
		"cortex_vm_commitments_total",
		"Number of virtual machine commitments",
		[]string{
			"resource_name", // "cores", "ram", "instances_<my_flavor>", ...
			"availability_zone",
			"status",
		},
		nil,
	)
	k.vmCommitmentsSumDesc = prometheus.NewDesc(
		"cortex_vm_commitments_sum",
		"Total sum of virtual machine commitments",
		[]string{
			"resource_name", // "cores", "ram", "instances_<my_flavor>", ...
			"availability_zone",
			"status",
		},
		nil,
	)
	k.committedCoresDesc = prometheus.NewDesc(
		"cortex_vm_commitments_cores_sum",
		"Total number of committed cores (raw demand, not actual allocation with overcommit)",
		[]string{
			"resource_name", // "cores", "ram", "instances_<my_flavor>", ...
			"availability_zone",
			"status",
		},
		nil,
	)
	k.committedMemoryDesc = prometheus.NewDesc(
		"cortex_vm_commitments_memory_sum",
		"Total amount of committed memory in bytes (raw demand, not actual allocation with overcommit)",
		[]string{
			"resource_name", // "cores", "ram", "instances_<my_flavor>", ...
			"availability_zone",
			"status",
		},
		nil,
	)
	return nil
}

func (k *VMCommitmentsKPI) Describe(ch chan<- *prometheus.Desc) {
	ch <- k.vmCommitmentsTotalDesc
	ch <- k.vmCommitmentsSumDesc
	ch <- k.committedCoresDesc
	ch <- k.committedMemoryDesc
}

// Convert limes memory units to bytes for commitments.
func convertLimesMemory(unit string) (float64, error) {
	switch unit {
	case "B", "":
		return 1, nil
	case "KiB":
		return 1024, nil
	case "MiB":
		return 1024 * 1024, nil
	case "GiB":
		return 1024 * 1024 * 1024, nil
	case "TiB":
		return 1024 * 1024 * 1024 * 1024, nil
	default:
		return 0, fmt.Errorf("unknown memory unit: %s", unit)
	}
}

func (k *VMCommitmentsKPI) Collect(ch chan<- prometheus.Metric) {
	var commitments []limes.Commitment
	table := limes.Commitment{}.TableName()
	if _, err := k.DB.Select(&commitments, `
        SELECT * FROM `+table+`
        WHERE service_type = 'compute'
    `); err != nil {
		slog.Error("failed to load commitments", "err", err)
		return
	}
	var flavors []nova.Flavor
	table = nova.Flavor{}.TableName()
	if _, err := k.DB.Select(&flavors, "SELECT * FROM "+table); err != nil {
		slog.Error("failed to load flavors", "err", err)
		return
	}
	flavorsByName := make(map[string]nova.Flavor, len(flavors))
	for _, flavor := range flavors {
		flavorsByName[flavor.Name] = flavor
	}
	sep := "⏭️" // Use a separator that is unlikely to appear in resource names.
	statsVMCommitmentsCount := map[string]float64{}
	statsVMCommitmentsSum := map[string]float64{}
	statsCommittedCores := map[string]float64{}
	statsCommittedMemory := map[string]float64{}
	for _, commitment := range commitments {
		key := commitment.ResourceName + sep +
			commitment.AvailabilityZone + sep +
			commitment.Status

		switch {
		case strings.HasPrefix(commitment.ResourceName, "instances_"):
			// Note: This does not consider overcommit, so it only returns
			// the demand, not the allocation when placed somewhere.
			flavor, ok := flavorsByName[commitment.ResourceName[len("instances_"):]]
			if !ok {
				slog.Warn("vm_commitments: unknown flavor in commitment", "flavor", commitment.ResourceName)
				continue
			}
			cores, memory := float64(flavor.VCPUs), float64(flavor.RAM)*1024*1024
			statsVMCommitmentsCount[key] += 1
			statsVMCommitmentsSum[key] += 1 // number of instances
			statsCommittedCores[key] += cores
			statsCommittedMemory[key] += memory
		case commitment.ResourceName == "cores":
			cores := float64(commitment.Amount)
			statsVMCommitmentsCount[key] += 1
			statsVMCommitmentsSum[key] += cores
			statsCommittedCores[key] += cores
		case commitment.ResourceName == "ram":
			var err error
			memory, err := convertLimesMemory(commitment.Unit)
			if err != nil {
				slog.Warn("vm_commitments: failed to convert memory unit", "unit", commitment.Unit, "err", err)
				continue
			}
			memory *= float64(commitment.Amount)
			statsVMCommitmentsCount[key] += 1
			statsVMCommitmentsSum[key] += memory // converted to bytes
			statsCommittedMemory[key] += memory
		default:
			slog.Warn("vm_commitments: unknown resource name in commitment", "resource_name", commitment.ResourceName)
			continue
		}
	}

	for key, count := range statsVMCommitmentsCount {
		parts := strings.SplitN(key, sep, 3)
		ch <- prometheus.MustNewConstMetric(
			k.vmCommitmentsTotalDesc,
			prometheus.CounterValue,
			count,
			parts[0], // resource_name
			parts[1], // availability_zone
			parts[2], // status
		)
	}
	for key, sum := range statsVMCommitmentsSum {
		parts := strings.SplitN(key, sep, 3)
		ch <- prometheus.MustNewConstMetric(
			k.vmCommitmentsSumDesc,
			prometheus.CounterValue,
			sum,
			parts[0], // resource_name
			parts[1], // availability_zone
			parts[2], // status
		)
	}
	for key, cores := range statsCommittedCores {
		parts := strings.SplitN(key, sep, 3)
		ch <- prometheus.MustNewConstMetric(
			k.committedCoresDesc,
			prometheus.GaugeValue,
			cores,
			parts[0], // resource_name
			parts[1], // availability_zone
			parts[2], // status
		)
	}
	for key, memory := range statsCommittedMemory {
		parts := strings.SplitN(key, sep, 3)
		ch <- prometheus.MustNewConstMetric(
			k.committedMemoryDesc,
			prometheus.GaugeValue,
			memory,
			parts[0], // resource_name
			parts[1], // availability_zone
			parts[2], // status
		)
	}
}
