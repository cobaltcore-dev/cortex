// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package sap

import (
	"log/slog"
	"strconv"

	"github.com/cobaltcore-dev/cortex/extractor/api/features/sap"
	"github.com/cobaltcore-dev/cortex/extractor/api/features/shared"

	"github.com/cobaltcore-dev/cortex/kpis/internal/plugins"
	"github.com/cobaltcore-dev/cortex/lib/conf"
	"github.com/cobaltcore-dev/cortex/lib/db"
	"github.com/prometheus/client_golang/prometheus"
)

type HostRunningVMs struct {
	ComputeHostName  string  `db:"compute_host"`
	AvailabilityZone string  `db:"availability_zone"`
	CPUArchitecture  string  `db:"cpu_architecture"`
	HypervisorFamily string  `db:"hypervisor_family"`
	WorkloadType     string  `db:"workload_type"`
	Enabled          bool    `db:"enabled"`
	PinnedProjects   string  `db:"pinned_projects"`
	RunningVMs       float64 `db:"running_vms"`
	shared.HostUtilization
}

type HostRunningVMsKPI struct {
	// Common base for all KPIs that provides standard functionality.
	plugins.BaseKPI[struct{}] // No options passed through yaml config

	hostRunningVMsPerHost *prometheus.Desc
}

func (HostRunningVMsKPI) GetName() string {
	return "sap_host_running_vms_kpi"
}

func (k *HostRunningVMsKPI) Init(db db.DB, opts conf.RawOpts) error {
	if err := k.BaseKPI.Init(db, opts); err != nil {
		return err
	}
	k.hostRunningVMsPerHost = prometheus.NewDesc(
		"cortex_sap_running_vms_per_host",
		"Current amount of running virtual machines on a host.",
		[]string{
			"compute_host",
			"availability_zone",
			"cpu_architecture",
			"workload_type",
			"hypervisor_family",
			"enabled",
			"pinned_projects",
		},
		nil,
	)
	return nil
}

func (k *HostRunningVMsKPI) Describe(ch chan<- *prometheus.Desc) {
	ch <- k.hostRunningVMsPerHost
}

func (k *HostRunningVMsKPI) Collect(ch chan<- prometheus.Metric) {
	var hostRunningVMs []HostRunningVMs

	// We NEED to join with host_utilization to filter out hosts that do not report any capacity.
	// We are using a LEFT JOIN with host_details to be able to log out hosts that are filtered out due to zero capacity.
	query := `
		SELECT
    		hd.compute_host,
    		hd.availability_zone,
    		hd.cpu_architecture,
    		hd.hypervisor_family,
    		hd.workload_type,
    		hd.enabled,
			COALESCE(hd.pinned_projects, '') AS pinned_projects,
    		hd.running_vms,
			COALESCE(hu.total_ram_allocatable_mb, 0) AS total_ram_allocatable_mb,
			COALESCE(hu.total_vcpus_allocatable, 0) AS total_vcpus_allocatable,
			COALESCE(hu.total_disk_allocatable_gb, 0) AS total_disk_allocatable_gb
		FROM ` + sap.HostDetails{}.TableName() + ` AS hd
		LEFT JOIN ` + shared.HostUtilization{}.TableName() + ` AS hu
		    ON hu.compute_host = hd.compute_host
		WHERE hypervisor_type != 'ironic';
    `
	if _, err := k.DB.Select(&hostRunningVMs, query); err != nil {
		slog.Error("failed to select host details", "err", err)
		return
	}

	for _, host := range hostRunningVMs {
		if host.TotalRAMAllocatableMB == 0 || host.TotalVCPUsAllocatable == 0 || host.TotalDiskAllocatableGB == 0 {
			slog.Info(
				"Skipping host since placement is reporting zero allocatable resources",
				"metric", "cortex_sap_running_vms_per_host",
				"host", host.ComputeHostName,
				"cpu", host.TotalVCPUsAllocatable,
				"ram", host.TotalRAMAllocatableMB,
				"disk", host.TotalDiskAllocatableGB,
			)
			continue
		}

		enabled := strconv.FormatBool(host.Enabled)

		ch <- prometheus.MustNewConstMetric(
			k.hostRunningVMsPerHost,
			prometheus.GaugeValue,
			host.RunningVMs,
			host.ComputeHostName,
			host.AvailabilityZone,
			host.CPUArchitecture,
			host.WorkloadType,
			host.HypervisorFamily,
			enabled,
			host.PinnedProjects,
		)
	}
}
