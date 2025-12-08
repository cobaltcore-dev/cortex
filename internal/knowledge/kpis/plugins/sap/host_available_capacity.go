// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package sap

import (
	"context"
	"log/slog"
	"strconv"

	"github.com/cobaltcore-dev/cortex/api/v1alpha1"
	"github.com/cobaltcore-dev/cortex/internal/knowledge/extractor/plugins/sap"
	"github.com/cobaltcore-dev/cortex/internal/knowledge/extractor/plugins/shared"
	"github.com/cobaltcore-dev/cortex/pkg/tools"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/cobaltcore-dev/cortex/internal/knowledge/kpis/plugins"
	"github.com/cobaltcore-dev/cortex/pkg/conf"
	"github.com/cobaltcore-dev/cortex/pkg/db"
	"github.com/prometheus/client_golang/prometheus"
)

type HostAvailableCapacityKPI struct {
	// Common base for all KPIs that provides standard functionality.
	plugins.BaseKPI[struct{}] // No options passed through yaml config

	hostResourcesAvailableCapacityPerHost    *prometheus.Desc
	hostResourcesAvailableCapacityPerHostPct *prometheus.Desc
	hostResourcesAvailableCapacityHist       *prometheus.Desc
}

func (HostAvailableCapacityKPI) GetName() string {
	return "sap_host_capacity_kpi"
}

func (k *HostAvailableCapacityKPI) Init(db *db.DB, client client.Client, opts conf.RawOpts) error {
	if err := k.BaseKPI.Init(db, client, opts); err != nil {
		return err
	}
	k.hostResourcesAvailableCapacityPerHost = prometheus.NewDesc(
		"cortex_sap_available_capacity_per_host",
		"Available capacity per resource on the hosts currently (individually by host).",
		[]string{
			"compute_host",
			"resource",
			"availability_zone",
			"cpu_architecture",
			"workload_type",
			"hypervisor_family",
			"enabled",
			"decommissioned",
			"external_customer",
			"disabled_reason",
			"pinned_projects",
		},
		nil,
	)
	k.hostResourcesAvailableCapacityPerHostPct = prometheus.NewDesc(
		"cortex_sap_available_capacity_per_host_pct",
		"Available capacity (%) per resource on the hosts currently (individually by host).",
		[]string{
			"compute_host",
			"resource",
			"availability_zone",
			"cpu_architecture",
			"workload_type",
			"hypervisor_family",
			"enabled",
			"decommissioned",
			"external_customer",
			"disabled_reason",
			"pinned_projects",
		},
		nil,
	)
	k.hostResourcesAvailableCapacityHist = prometheus.NewDesc(
		"cortex_sap_available_capacity_pct",
		"Available resource capacity on the hosts currently (aggregated as a histogram).",
		[]string{"resource"},
		nil,
	)
	return nil
}

func (k *HostAvailableCapacityKPI) Describe(ch chan<- *prometheus.Desc) {
	ch <- k.hostResourcesAvailableCapacityPerHost
	ch <- k.hostResourcesAvailableCapacityHist
	ch <- k.hostResourcesAvailableCapacityPerHostPct
}

func (k *HostAvailableCapacityKPI) Collect(ch chan<- prometheus.Metric) {
	hostDetailsKnowledge := &v1alpha1.Knowledge{}
	if err := k.Client.Get(
		context.Background(),
		client.ObjectKey{Name: "sap-host-details"},
		hostDetailsKnowledge,
	); err != nil {
		slog.Error("failed to get knowledge sap-host-details", "err", err)
		return
	}
	hostDetails, err := v1alpha1.
		UnboxFeatureList[sap.HostDetails](hostDetailsKnowledge.Status.Raw)
	if err != nil {
		slog.Error("failed to unbox storage pool cpu usage", "err", err)
		return
	}
	detailsByComputeHost := make(map[string]sap.HostDetails)
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
		UnboxFeatureList[shared.HostUtilization](hostUtilizationKnowledge.Status.Raw)
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

		if utilization.TotalRAMAllocatableMB == 0 || utilization.TotalVCPUsAllocatable == 0 || utilization.TotalDiskAllocatableGB == 0 {
			slog.Info(
				"Skipping host since placement is reporting zero allocatable resources",
				"metric", "cortex_sap_available_capacity_per_host",
				"host", utilization.ComputeHost,
				"cpu", utilization.TotalVCPUsAllocatable,
				"ram", utilization.TotalRAMAllocatableMB,
				"disk", utilization.TotalDiskAllocatableGB,
			)
			continue
		}

		enabled := strconv.FormatBool(detail.Enabled)
		decommissioned := strconv.FormatBool(detail.Decommissioned)
		externalCustomer := strconv.FormatBool(detail.ExternalCustomer)
		pinnedProjects := ""
		if detail.PinnedProjects != nil {
			pinnedProjects = *detail.PinnedProjects
		}
		disabledReason := "-"
		if detail.DisabledReason != nil {
			disabledReason = *detail.DisabledReason
		}

		ch <- prometheus.MustNewConstMetric(
			k.hostResourcesAvailableCapacityPerHost,
			prometheus.GaugeValue,
			utilization.TotalRAMAllocatableMB-utilization.RAMUsedMB,
			utilization.ComputeHost,
			"ram",
			detail.AvailabilityZone,
			detail.CPUArchitecture,
			detail.WorkloadType,
			detail.HypervisorFamily,
			enabled,
			decommissioned,
			externalCustomer,
			disabledReason,
			pinnedProjects,
		)
		ch <- prometheus.MustNewConstMetric(
			k.hostResourcesAvailableCapacityPerHostPct,
			prometheus.GaugeValue,
			(utilization.TotalRAMAllocatableMB-utilization.RAMUsedMB)/utilization.TotalRAMAllocatableMB*100,
			utilization.ComputeHost,
			"ram",
			detail.AvailabilityZone,
			detail.CPUArchitecture,
			detail.WorkloadType,
			detail.HypervisorFamily,
			enabled,
			decommissioned,
			externalCustomer,
			disabledReason,
			pinnedProjects,
		)
		ch <- prometheus.MustNewConstMetric(
			k.hostResourcesAvailableCapacityPerHost,
			prometheus.GaugeValue,
			utilization.TotalVCPUsAllocatable-utilization.VCPUsUsed,
			utilization.ComputeHost,
			"cpu",
			detail.AvailabilityZone,
			detail.CPUArchitecture,
			detail.WorkloadType,
			detail.HypervisorFamily,
			enabled,
			decommissioned,
			externalCustomer,
			disabledReason,
			pinnedProjects,
		)
		ch <- prometheus.MustNewConstMetric(
			k.hostResourcesAvailableCapacityPerHostPct,
			prometheus.GaugeValue,
			(utilization.TotalVCPUsAllocatable-utilization.VCPUsUsed)/utilization.TotalVCPUsAllocatable*100,
			utilization.ComputeHost,
			"cpu",
			detail.AvailabilityZone,
			detail.CPUArchitecture,
			detail.WorkloadType,
			detail.HypervisorFamily,
			enabled,
			decommissioned,
			externalCustomer,
			disabledReason,
			pinnedProjects,
		)
		ch <- prometheus.MustNewConstMetric(
			k.hostResourcesAvailableCapacityPerHost,
			prometheus.GaugeValue,
			utilization.TotalDiskAllocatableGB-utilization.DiskUsedGB,
			utilization.ComputeHost,
			"disk",
			detail.AvailabilityZone,
			detail.CPUArchitecture,
			detail.WorkloadType,
			detail.HypervisorFamily,
			enabled,
			decommissioned,
			externalCustomer,
			disabledReason,
			pinnedProjects,
		)
		ch <- prometheus.MustNewConstMetric(
			k.hostResourcesAvailableCapacityPerHostPct,
			prometheus.GaugeValue,
			(utilization.TotalDiskAllocatableGB-utilization.DiskUsedGB)/utilization.TotalDiskAllocatableGB*100,
			utilization.ComputeHost,
			"disk",
			detail.AvailabilityZone,
			detail.CPUArchitecture,
			detail.WorkloadType,
			detail.HypervisorFamily,
			enabled,
			decommissioned,
			externalCustomer,
			disabledReason,
			pinnedProjects,
		)
	}

	buckets := prometheus.LinearBuckets(0, 5, 20)
	// Histogram for CPU
	keysFunc := func(hs shared.HostUtilization) []string { return []string{"cpu"} }
	valueFunc := func(hs shared.HostUtilization) float64 {
		return (hs.TotalVCPUsAllocatable - hs.VCPUsUsed) / hs.TotalVCPUsAllocatable * 100
	}
	hists, counts, sums := tools.Histogram(hostUtilizations, buckets, keysFunc, valueFunc)
	for key, hist := range hists {
		ch <- prometheus.MustNewConstHistogram(k.hostResourcesAvailableCapacityHist, counts[key], sums[key], hist, key)
	}
	// Histogram for RAM
	keysFunc = func(hs shared.HostUtilization) []string { return []string{"ram"} }
	valueFunc = func(hs shared.HostUtilization) float64 {
		return (hs.TotalRAMAllocatableMB - hs.RAMUsedMB) / hs.TotalRAMAllocatableMB * 100
	}
	hists, counts, sums = tools.Histogram(hostUtilizations, buckets, keysFunc, valueFunc)
	for key, hist := range hists {
		ch <- prometheus.MustNewConstHistogram(k.hostResourcesAvailableCapacityHist, counts[key], sums[key], hist, key)
	}
	// Histogram for Disk
	keysFunc = func(hs shared.HostUtilization) []string { return []string{"disk"} }
	valueFunc = func(hs shared.HostUtilization) float64 {
		return (hs.TotalDiskAllocatableGB - hs.DiskUsedGB) / hs.TotalDiskAllocatableGB * 100
	}
	hists, counts, sums = tools.Histogram(hostUtilizations, buckets, keysFunc, valueFunc)
	for key, hist := range hists {
		ch <- prometheus.MustNewConstHistogram(k.hostResourcesAvailableCapacityHist, counts[key], sums[key], hist, key)
	}
}
