// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package compute

import (
	"context"
	"log/slog"
	"strconv"

	"github.com/cobaltcore-dev/cortex/api/v1alpha1"
	"github.com/cobaltcore-dev/cortex/internal/knowledge/extractor/plugins/compute"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/cobaltcore-dev/cortex/internal/knowledge/kpis/plugins"
	"github.com/cobaltcore-dev/cortex/pkg/conf"
	"github.com/cobaltcore-dev/cortex/pkg/db"
	"github.com/prometheus/client_golang/prometheus"
)

type VMwareResourceCapacityKPI struct {
	// Common base for all KPIs that provides standard functionality.
	plugins.BaseKPI[struct{}] // No options passed through yaml config

	availableCapacityPerHost *prometheus.Desc
	totalCapacityPerHost     *prometheus.Desc
}

func (VMwareResourceCapacityKPI) GetName() string {
	return "vmware_host_capacity_kpi"
}

func (k *VMwareResourceCapacityKPI) Init(db *db.DB, client client.Client, opts conf.RawOpts) error {
	if err := k.BaseKPI.Init(db, client, opts); err != nil {
		return err
	}
	k.availableCapacityPerHost = prometheus.NewDesc(
		"cortex_vmware_host_capacity_available",
		"Available capacity per resource on the hosts currently (individually by host).",
		[]string{
			"compute_host",
			"resource",
			"availability_zone",
			"cpu_architecture",
			"workload_type",
			"enabled",
			"decommissioned",
			"external_customer",
			"pinned_projects",
			"disabled_reason",
			"pinned_project_ids",
		},
		nil,
	)
	k.totalCapacityPerHost = prometheus.NewDesc(
		"cortex_vmware_host_capacity_total",
		"Total resources available on the hosts currently (individually by host).",
		[]string{
			"compute_host",
			"resource",
			"availability_zone",
			"cpu_architecture",
			"workload_type",
			"enabled",
			"decommissioned",
			"external_customer",
			"pinned_projects",
			"pinned_project_ids",
		},
		nil,
	)
	return nil
}

func (k *VMwareResourceCapacityKPI) Describe(ch chan<- *prometheus.Desc) {
	ch <- k.availableCapacityPerHost
	ch <- k.totalCapacityPerHost
}

func (k *VMwareResourceCapacityKPI) Collect(ch chan<- prometheus.Metric) {
	hostDetailsKnowledge := &v1alpha1.Knowledge{}
	if err := k.Client.Get(
		context.Background(),
		client.ObjectKey{Name: "host-details"},
		hostDetailsKnowledge,
	); err != nil {
		slog.Error("failed to get knowledge host-details", "err", err)
		return
	}
	hostDetails, err := v1alpha1.
		UnboxFeatureList[compute.HostDetails](hostDetailsKnowledge.Status.Raw)
	if err != nil {
		slog.Error("failed to unbox storage pool cpu usage", "err", err)
		return
	}
	detailsByComputeHost := make(map[string]compute.HostDetails)
	for _, detail := range hostDetails {
		detailsByComputeHost[detail.ComputeHost] = detail
	}

	hostUtilizationKnowledge := &v1alpha1.Knowledge{}
	if err := k.Client.Get(
		context.Background(),
		client.ObjectKey{Name: "host-utilization"},
		hostUtilizationKnowledge,
	); err != nil {
		slog.Error("failed to get knowledge host-utilization", "err", err)
		return
	}
	hostUtilizations, err := v1alpha1.
		UnboxFeatureList[compute.HostUtilization](hostUtilizationKnowledge.Status.Raw)
	if err != nil {
		slog.Error("failed to unbox host utilization", "err", err)
		return
	}

	for _, utilization := range hostUtilizations {
		detail, exists := detailsByComputeHost[utilization.ComputeHost]
		if !exists {
			slog.Warn("host_available_capacity: missing host details for compute host", "compute_host", utilization.ComputeHost)
			continue
		}
		if detail.HypervisorType == "ironic" {
			continue // Ironic hosts do not run VMs/instances
		}

		if detail.HypervisorFamily != "vmware" {
			continue
		}

		if utilization.TotalRAMAllocatableMB == 0 || utilization.TotalVCPUsAllocatable == 0 || utilization.TotalDiskAllocatableGB == 0 {
			slog.Info(
				"Skipping host since placement is reporting zero allocatable resources",
				"metric", "cortex_available_capacity_per_host",
				"host", utilization.ComputeHost,
				"cpu", utilization.TotalVCPUsAllocatable,
				"ram", utilization.TotalRAMAllocatableMB,
				"disk", utilization.TotalDiskAllocatableGB,
			)
			continue
		}

		availableCPUs := float64(utilization.TotalVCPUsAllocatable - utilization.VCPUsUsed)
		availableRAMMB := float64(utilization.TotalRAMAllocatableMB - utilization.RAMUsedMB)
		availableDiskGB := float64(utilization.TotalDiskAllocatableGB - utilization.DiskUsedGB)

		k.exportCapacityMetricVMware(ch, "cpu", availableCPUs, utilization.TotalVCPUsAllocatable, detail)
		k.exportCapacityMetricVMware(ch, "ram", availableRAMMB, utilization.TotalRAMAllocatableMB, detail)
		k.exportCapacityMetricVMware(ch, "disk", availableDiskGB, utilization.TotalDiskAllocatableGB, detail)
	}
}

func (k *VMwareResourceCapacityKPI) exportCapacityMetricVMware(ch chan<- prometheus.Metric, resource string, available, total float64, host compute.HostDetails) {
	enabled := strconv.FormatBool(host.Enabled)
	decommissioned := strconv.FormatBool(host.Decommissioned)
	externalCustomer := strconv.FormatBool(host.ExternalCustomer)
	pinnedProjectIds := ""
	pinnedProjects := "false"
	if host.PinnedProjects != nil {
		pinnedProjectIds = *host.PinnedProjects
		pinnedProjects = "true"
	}

	disabledReason := "-"
	if host.DisabledReason != nil {
		disabledReason = *host.DisabledReason
	}

	ch <- prometheus.MustNewConstMetric(
		k.availableCapacityPerHost,
		prometheus.GaugeValue,
		available,
		host.ComputeHost,
		resource,
		host.AvailabilityZone,
		host.CPUArchitecture,
		host.WorkloadType,
		enabled,
		decommissioned,
		externalCustomer,
		pinnedProjects,
		disabledReason,
		pinnedProjectIds,
	)

	ch <- prometheus.MustNewConstMetric(
		k.totalCapacityPerHost,
		prometheus.GaugeValue,
		total,
		host.ComputeHost,
		resource,
		host.AvailabilityZone,
		host.CPUArchitecture,
		host.WorkloadType,
		enabled,
		decommissioned,
		externalCustomer,
		pinnedProjects,
		pinnedProjectIds,
	)
}
