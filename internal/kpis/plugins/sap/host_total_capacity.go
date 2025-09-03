// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package sap

import (
	"log/slog"
	"strconv"

	"github.com/cobaltcore-dev/cortex/internal/extractor/plugins/sap"
	"github.com/cobaltcore-dev/cortex/internal/extractor/plugins/shared"

	"github.com/cobaltcore-dev/cortex/internal/conf"
	"github.com/cobaltcore-dev/cortex/internal/db"
	"github.com/cobaltcore-dev/cortex/internal/kpis/plugins"
	"github.com/prometheus/client_golang/prometheus"
)

type HostTotalCapacityKPI struct {
	// Common base for all KPIs that provides standard functionality.
	plugins.BaseKPI[struct{}] // No options passed through yaml config
	hostTotalCapacityPerHost  *prometheus.Desc
}

func (HostTotalCapacityKPI) GetName() string {
	return "sap_host_total_capacity_kpi"
}

func (k *HostTotalCapacityKPI) Init(db db.DB, opts conf.RawOpts) error {
	if err := k.BaseKPI.Init(db, opts); err != nil {
		return err
	}
	k.hostTotalCapacityPerHost = prometheus.NewDesc(
		"cortex_sap_total_capacity_per_host",
		"Total resources available on the hosts currently (individually by host).",
		[]string{
			"compute_host",
			"resource",
			"availability_zone",
			"cpu_architecture",
			"workload_type",
			"hypervisor_family",
			"enabled",
		},
		nil,
	)
	return nil
}

func (k *HostTotalCapacityKPI) Describe(ch chan<- *prometheus.Desc) {
	ch <- k.hostTotalCapacityPerHost
}

func (k *HostTotalCapacityKPI) Collect(ch chan<- prometheus.Metric) {
	type HostTotalCapacityPerAvailabilityZone struct {
		ComputeHostName  string  `db:"compute_host"`
		AvailabilityZone string  `db:"availability_zone"`
		RunningVMs       int     `db:"running_vms"`
		CPUArchitecture  string  `db:"cpu_architecture"`
		HypervisorFamily string  `db:"hypervisor_family"`
		WorkloadType     string  `db:"workload_type"`
		Enabled          bool    `db:"enabled"`
		DisabledReason   *string `db:"disabled_reason"`
		shared.HostUtilization
	}

	var hostTotalCapacity []HostTotalCapacityPerAvailabilityZone

	query := `
		SELECT
    		hd.compute_host,
    		hd.availability_zone,
    		hd.running_vms,
    		hd.cpu_architecture,
    		hd.hypervisor_family,
    		hd.workload_type,
    		hd.enabled,
    		COALESCE(hu.ram_utilized_pct, 0) AS ram_utilized_pct,
			COALESCE(hu.vcpus_utilized_pct, 0) AS vcpus_utilized_pct,
			COALESCE(hu.disk_utilized_pct, 0) AS disk_utilized_pct,
			COALESCE(hu.total_ram_allocatable_mb, 0) AS total_ram_allocatable_mb,
			COALESCE(hu.total_vcpus_allocatable, 0) AS total_vcpus_allocatable,
			COALESCE(hu.total_disk_allocatable_gb, 0) AS total_disk_allocatable_gb
		FROM ` + sap.HostDetails{}.TableName() + ` AS hd
		LEFT JOIN ` + shared.HostUtilization{}.TableName() + ` AS hu
		    ON hu.compute_host = hd.compute_host
		WHERE hd.hypervisor_type != 'ironic';
    `
	if _, err := k.DB.Select(&hostTotalCapacity, query); err != nil {
		slog.Error("failed to select host total capacity", "err", err)
		return
	}

	for _, host := range hostTotalCapacity {
		enabled := strconv.FormatBool(host.Enabled)

		ch <- prometheus.MustNewConstMetric(
			k.hostTotalCapacityPerHost,
			prometheus.GaugeValue,
			host.TotalRAMAllocatableMB,
			host.ComputeHostName,
			"ram",
			host.AvailabilityZone,
			host.CPUArchitecture,
			host.WorkloadType,
			host.HypervisorFamily,
			enabled,
		)

		ch <- prometheus.MustNewConstMetric(
			k.hostTotalCapacityPerHost,
			prometheus.GaugeValue,
			host.TotalVCPUsAllocatable,
			host.ComputeHostName,
			"cpu",
			host.AvailabilityZone,
			host.CPUArchitecture,
			host.WorkloadType,
			host.HypervisorFamily,
			enabled,
		)

		ch <- prometheus.MustNewConstMetric(
			k.hostTotalCapacityPerHost,
			prometheus.GaugeValue,
			host.TotalDiskAllocatableGB,
			host.ComputeHostName,
			"disk",
			host.AvailabilityZone,
			host.CPUArchitecture,
			host.WorkloadType,
			host.HypervisorFamily,
			enabled,
		)
	}
}
