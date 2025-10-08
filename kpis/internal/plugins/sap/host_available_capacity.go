// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package sap

import (
	"log/slog"
	"strconv"

	"github.com/cobaltcore-dev/cortex/extractor/api/features/sap"
	"github.com/cobaltcore-dev/cortex/extractor/api/features/shared"
	"github.com/cobaltcore-dev/cortex/internal/tools"

	"github.com/cobaltcore-dev/cortex/internal/conf"
	"github.com/cobaltcore-dev/cortex/internal/db"
	"github.com/cobaltcore-dev/cortex/kpis/internal/plugins"
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

func (k *HostAvailableCapacityKPI) Init(db db.DB, opts conf.RawOpts) error {
	if err := k.BaseKPI.Init(db, opts); err != nil {
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
	type HostUtilizationPerAvailabilityZone struct {
		ComputeHostName  string `db:"compute_host"`
		AvailabilityZone string `db:"availability_zone"`
		CPUArchitecture  string `db:"cpu_architecture"`
		HypervisorFamily string `db:"hypervisor_family"`
		WorkloadType     string `db:"workload_type"`
		Enabled          bool   `db:"enabled"`
		DisabledReason   string `db:"disabled_reason"`
		PinnedProjects   string `db:"pinned_projects"`
		shared.HostUtilization
	}

	var hostUtilization []HostUtilizationPerAvailabilityZone

	// Include hosts with no usage data (LEFT JOIN) so that we can log out hosts that are filtered out due to zero capacity
	// Also exclude ironic hosts as they do not run VMs/instances
	query := `
		SELECT
    		hd.compute_host,
    		hd.availability_zone,
    		hd.cpu_architecture,
    		hd.hypervisor_family,
    		hd.workload_type,
    		hd.enabled,
    		COALESCE(hd.disabled_reason, '-') AS disabled_reason,
			COALESCE(hd.pinned_projects, '') AS pinned_projects,
			COALESCE(hu.ram_used_mb, 0) AS ram_used_mb,
			COALESCE(hu.vcpus_used, 0) AS vcpus_used,
			COALESCE(hu.disk_used_gb, 0) AS disk_used_gb,
			COALESCE(hu.total_ram_allocatable_mb, 0) AS total_ram_allocatable_mb,
			COALESCE(hu.total_vcpus_allocatable, 0) AS total_vcpus_allocatable,
			COALESCE(hu.total_disk_allocatable_gb, 0) AS total_disk_allocatable_gb
		FROM ` + sap.HostDetails{}.TableName() + ` AS hd
		LEFT JOIN ` + shared.HostUtilization{}.TableName() + ` AS hu
		    ON hu.compute_host = hd.compute_host
		WHERE hd.hypervisor_type != 'ironic';
    `
	if _, err := k.DB.Select(&hostUtilization, query); err != nil {
		slog.Error("failed to select host utilization", "err", err)
		return
	}

	for _, host := range hostUtilization {
		if host.TotalRAMAllocatableMB == 0 || host.TotalVCPUsAllocatable == 0 || host.TotalDiskAllocatableGB == 0 {
			slog.Info(
				"Skipping host since placement is reporting zero allocatable resources",
				"metric", "cortex_sap_available_capacity_per_host",
				"host", host.ComputeHostName,
				"cpu", host.TotalVCPUsAllocatable,
				"ram", host.TotalRAMAllocatableMB,
				"disk", host.TotalDiskAllocatableGB,
			)
			continue
		}

		enabled := strconv.FormatBool(host.Enabled)

		ch <- prometheus.MustNewConstMetric(
			k.hostResourcesAvailableCapacityPerHost,
			prometheus.GaugeValue,
			host.TotalRAMAllocatableMB-host.RAMUsedMB,
			host.ComputeHostName,
			"ram",
			host.AvailabilityZone,
			host.CPUArchitecture,
			host.WorkloadType,
			host.HypervisorFamily,
			enabled,
			host.DisabledReason,
			host.PinnedProjects,
		)
		ch <- prometheus.MustNewConstMetric(
			k.hostResourcesAvailableCapacityPerHostPct,
			prometheus.GaugeValue,
			(host.TotalRAMAllocatableMB-host.RAMUsedMB)/host.TotalRAMAllocatableMB*100,
			host.ComputeHostName,
			"ram",
			host.AvailabilityZone,
			host.CPUArchitecture,
			host.WorkloadType,
			host.HypervisorFamily,
			enabled,
			host.DisabledReason,
			host.PinnedProjects,
		)
		ch <- prometheus.MustNewConstMetric(
			k.hostResourcesAvailableCapacityPerHost,
			prometheus.GaugeValue,
			host.TotalVCPUsAllocatable-host.VCPUsUsed,
			host.ComputeHostName,
			"cpu",
			host.AvailabilityZone,
			host.CPUArchitecture,
			host.WorkloadType,
			host.HypervisorFamily,
			enabled,
			host.DisabledReason,
			host.PinnedProjects,
		)
		ch <- prometheus.MustNewConstMetric(
			k.hostResourcesAvailableCapacityPerHostPct,
			prometheus.GaugeValue,
			(host.TotalVCPUsAllocatable-host.VCPUsUsed)/host.TotalVCPUsAllocatable*100,
			host.ComputeHostName,
			"cpu",
			host.AvailabilityZone,
			host.CPUArchitecture,
			host.WorkloadType,
			host.HypervisorFamily,
			enabled,
			host.DisabledReason,
			host.PinnedProjects,
		)
		ch <- prometheus.MustNewConstMetric(
			k.hostResourcesAvailableCapacityPerHost,
			prometheus.GaugeValue,
			host.TotalDiskAllocatableGB-host.DiskUsedGB,
			host.ComputeHostName,
			"disk",
			host.AvailabilityZone,
			host.CPUArchitecture,
			host.WorkloadType,
			host.HypervisorFamily,
			enabled,
			host.DisabledReason,
			host.PinnedProjects,
		)
		ch <- prometheus.MustNewConstMetric(
			k.hostResourcesAvailableCapacityPerHostPct,
			prometheus.GaugeValue,
			(host.TotalDiskAllocatableGB-host.DiskUsedGB)/host.TotalDiskAllocatableGB*100,
			host.ComputeHostName,
			"disk",
			host.AvailabilityZone,
			host.CPUArchitecture,
			host.WorkloadType,
			host.HypervisorFamily,
			enabled,
			host.DisabledReason,
			host.PinnedProjects,
		)
	}

	buckets := prometheus.LinearBuckets(0, 5, 20)
	// Histogram for CPU
	keysFunc := func(hs HostUtilizationPerAvailabilityZone) []string { return []string{"cpu"} }
	valueFunc := func(hs HostUtilizationPerAvailabilityZone) float64 {
		return (hs.TotalVCPUsAllocatable - hs.VCPUsUsed) / hs.TotalVCPUsAllocatable * 100
	}
	hists, counts, sums := tools.Histogram(hostUtilization, buckets, keysFunc, valueFunc)
	for key, hist := range hists {
		ch <- prometheus.MustNewConstHistogram(k.hostResourcesAvailableCapacityHist, counts[key], sums[key], hist, key)
	}
	// Histogram for RAM
	keysFunc = func(hs HostUtilizationPerAvailabilityZone) []string { return []string{"ram"} }
	valueFunc = func(hs HostUtilizationPerAvailabilityZone) float64 {
		return (hs.TotalRAMAllocatableMB - hs.RAMUsedMB) / hs.TotalRAMAllocatableMB * 100
	}
	hists, counts, sums = tools.Histogram(hostUtilization, buckets, keysFunc, valueFunc)
	for key, hist := range hists {
		ch <- prometheus.MustNewConstHistogram(k.hostResourcesAvailableCapacityHist, counts[key], sums[key], hist, key)
	}
	// Histogram for Disk
	keysFunc = func(hs HostUtilizationPerAvailabilityZone) []string { return []string{"disk"} }
	valueFunc = func(hs HostUtilizationPerAvailabilityZone) float64 {
		return (hs.TotalDiskAllocatableGB - hs.DiskUsedGB) / hs.TotalDiskAllocatableGB * 100
	}
	hists, counts, sums = tools.Histogram(hostUtilization, buckets, keysFunc, valueFunc)
	for key, hist := range hists {
		ch <- prometheus.MustNewConstHistogram(k.hostResourcesAvailableCapacityHist, counts[key], sums[key], hist, key)
	}
}
