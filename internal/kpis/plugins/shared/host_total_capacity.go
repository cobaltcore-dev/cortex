// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package shared

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
	return "host_total_capacity_kpi"
}

func (k *HostTotalCapacityKPI) Init(db db.DB, opts conf.RawOpts) error {
	if err := k.BaseKPI.Init(db, opts); err != nil {
		return err
	}
	k.hostTotalCapacityPerHost = prometheus.NewDesc(
		"cortex_total_capacity_per_host",
		"Total resources available on the hosts currently (individually by host).",
		[]string{
			"compute_host",
			"resource",
			"availability_zone",
			"cpu_architecture",
			"workload_type",
			"hypervisor_family",
			"enabled",
			"projects",
			"domains",
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
		ProjectNames     *string `db:"project_names"`
		DomainNames      *string `db:"domain_names"`
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
    		hdp.project_names,
    		hdp.domain_names,
    		COALESCE(hu.ram_utilized_pct, 0) AS ram_utilized_pct,
			COALESCE(hu.vcpus_utilized_pct, 0) AS vcpus_utilized_pct,
			COALESCE(hu.disk_utilized_pct, 0) AS disk_utilized_pct,
			COALESCE(hu.total_memory_allocatable_mb, 0) AS total_memory_allocatable_mb,
			COALESCE(hu.total_vcpus_allocatable, 0) AS total_vcpus_allocatable,
			COALESCE(hu.total_disk_allocatable_gb, 0) AS total_disk_allocatable_gb
		FROM ` + sap.HostDetails{}.TableName() + ` AS hd
		LEFT JOIN ` + shared.HostDomainProject{}.TableName() + ` AS hdp
		    ON hdp.compute_host = hd.compute_host
		LEFT JOIN ` + shared.HostUtilization{}.TableName() + ` AS hu
		    ON hu.compute_host = hd.compute_host
		WHERE hd.hypervisor_type != 'ironic';
    `
	if _, err := k.DB.Select(&hostTotalCapacity, query); err != nil {
		slog.Error("failed to select host total capacity", "err", err)
		return
	}

	for _, host := range hostTotalCapacity {
		projectNames := ""
		if host.ProjectNames != nil {
			projectNames = *host.ProjectNames
		}
		domainNames := ""
		if host.DomainNames != nil {
			domainNames = *host.DomainNames
		}

		enabled := strconv.FormatBool(host.Enabled)

		ch <- prometheus.MustNewConstMetric(
			k.hostTotalCapacityPerHost,
			prometheus.GaugeValue,
			host.TotalMemoryAllocatableMB,
			host.ComputeHostName,
			"ram",
			host.AvailabilityZone,
			host.CPUArchitecture,
			host.WorkloadType,
			host.HypervisorFamily,
			enabled,
			projectNames,
			domainNames,
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
			projectNames,
			domainNames,
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
			projectNames,
			domainNames,
		)
	}
}
