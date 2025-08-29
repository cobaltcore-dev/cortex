// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package sap

import (
	"log/slog"
	"strconv"

	"github.com/cobaltcore-dev/cortex/internal/extractor/plugins/sap"
	"github.com/cobaltcore-dev/cortex/internal/extractor/plugins/shared"
	"github.com/cobaltcore-dev/cortex/internal/tools"

	"github.com/cobaltcore-dev/cortex/internal/conf"
	"github.com/cobaltcore-dev/cortex/internal/db"
	"github.com/cobaltcore-dev/cortex/internal/kpis/plugins"
	"github.com/prometheus/client_golang/prometheus"
)

type HostUtilizationKPI struct {
	// Common base for all KPIs that provides standard functionality.
	plugins.BaseKPI[struct{}] // No options passed through yaml config

	hostResourcesUtilizedPerHost *prometheus.Desc
	hostResourcesUtilizedHist    *prometheus.Desc
}

func (HostUtilizationKPI) GetName() string {
	return "sap_host_utilization_kpi"
}

func (k *HostUtilizationKPI) Init(db db.DB, opts conf.RawOpts) error {
	if err := k.BaseKPI.Init(db, opts); err != nil {
		return err
	}
	k.hostResourcesUtilizedPerHost = prometheus.NewDesc(
		"cortex_sap_host_utilization_per_host_pct",
		"Resources utilized on the hosts currently (individually by host).",
		[]string{
			"compute_host",
			"resource",
			"availability_zone",
			"cpu_architecture",
			"workload_type",
			"hypervisor_family",
			"enabled",
			"disabled_reason",
		},
		nil,
	)
	k.hostResourcesUtilizedHist = prometheus.NewDesc(
		"cortex_sap_host_utilization_pct",
		"Resources utilized on the hosts currently (aggregated as a histogram).",
		[]string{"resource"},
		nil,
	)
	return nil
}

func (k *HostUtilizationKPI) Describe(ch chan<- *prometheus.Desc) {
	ch <- k.hostResourcesUtilizedPerHost
	ch <- k.hostResourcesUtilizedHist
}

func (k *HostUtilizationKPI) Collect(ch chan<- prometheus.Metric) {
	type HostUtilizationPerAvailabilityZone struct {
		ComputeHostName  string  `db:"compute_host"`
		AvailabilityZone string  `db:"availability_zone"`
		CPUArchitecture  string  `db:"cpu_architecture"`
		HypervisorFamily string  `db:"hypervisor_family"`
		WorkloadType     string  `db:"workload_type"`
		Enabled          bool    `db:"enabled"`
		DisabledReason   *string `db:"disabled_reason"`
		RAMUtilizedPct   float64 `db:"ram_utilized_pct"`
		VCPUsUtilizedPct float64 `db:"vcpus_utilized_pct"`
		DiskUtilizedPct  float64 `db:"disk_utilized_pct"`
	}

	var hostUtilization []HostUtilizationPerAvailabilityZone

	query := `
		SELECT
    		hd.compute_host,
    		hd.availability_zone,
    		hd.cpu_architecture,
    		hd.hypervisor_family,
    		hd.workload_type,
    		hd.enabled,
    		hd.disabled_reason,
    		COALESCE(hu.ram_utilized_pct, 0) AS ram_utilized_pct,
			COALESCE(hu.vcpus_utilized_pct, 0) AS vcpus_utilized_pct,
			COALESCE(hu.disk_utilized_pct, 0) AS disk_utilized_pct
		FROM ` + sap.HostDetails{}.TableName() + ` AS hd
		LEFT JOIN ` + shared.HostDomainProject{}.TableName() + ` AS hdp
		    ON hdp.compute_host = hd.compute_host
		LEFT JOIN ` + shared.HostUtilization{}.TableName() + ` AS hu
		    ON hu.compute_host = hd.compute_host
		WHERE hd.hypervisor_type != 'ironic';
    `
	if _, err := k.DB.Select(&hostUtilization, query); err != nil {
		slog.Error("failed to select host utilization", "err", err)
		return
	}

	for _, host := range hostUtilization {
		disabledReason := "-"
		if host.DisabledReason != nil {
			disabledReason = *host.DisabledReason
		}

		enabled := strconv.FormatBool(host.Enabled)

		ch <- prometheus.MustNewConstMetric(
			k.hostResourcesUtilizedPerHost,
			prometheus.GaugeValue,
			host.RAMUtilizedPct,
			host.ComputeHostName,
			"ram",
			host.AvailabilityZone,
			host.CPUArchitecture,
			host.WorkloadType,
			host.HypervisorFamily,
			enabled,
			disabledReason,
		)
		ch <- prometheus.MustNewConstMetric(
			k.hostResourcesUtilizedPerHost,
			prometheus.GaugeValue,
			host.VCPUsUtilizedPct,
			host.ComputeHostName,
			"cpu",
			host.AvailabilityZone,
			host.CPUArchitecture,
			host.WorkloadType,
			host.HypervisorFamily,
			enabled,
			disabledReason,
		)
		ch <- prometheus.MustNewConstMetric(
			k.hostResourcesUtilizedPerHost,
			prometheus.GaugeValue,
			host.DiskUtilizedPct,
			host.ComputeHostName,
			"disk",
			host.AvailabilityZone,
			host.CPUArchitecture,
			host.WorkloadType,
			host.HypervisorFamily,
			enabled,
			disabledReason,
		)
	}

	buckets := prometheus.LinearBuckets(0, 5, 20)
	// Histogram for CPU
	keysFunc := func(hs HostUtilizationPerAvailabilityZone) []string { return []string{"cpu"} }
	valueFunc := func(hs HostUtilizationPerAvailabilityZone) float64 { return hs.VCPUsUtilizedPct }
	hists, counts, sums := tools.Histogram(hostUtilization, buckets, keysFunc, valueFunc)
	for key, hist := range hists {
		ch <- prometheus.MustNewConstHistogram(k.hostResourcesUtilizedHist, counts[key], sums[key], hist, key)
	}
	// Histogram for Memory
	keysFunc = func(hs HostUtilizationPerAvailabilityZone) []string { return []string{"memory"} }
	valueFunc = func(hs HostUtilizationPerAvailabilityZone) float64 { return hs.RAMUtilizedPct }
	hists, counts, sums = tools.Histogram(hostUtilization, buckets, keysFunc, valueFunc)
	for key, hist := range hists {
		ch <- prometheus.MustNewConstHistogram(k.hostResourcesUtilizedHist, counts[key], sums[key], hist, key)
	}
	// Histogram for Disk
	keysFunc = func(hs HostUtilizationPerAvailabilityZone) []string { return []string{"disk"} }
	valueFunc = func(hs HostUtilizationPerAvailabilityZone) float64 { return hs.DiskUtilizedPct }
	hists, counts, sums = tools.Histogram(hostUtilization, buckets, keysFunc, valueFunc)
	for key, hist := range hists {
		ch <- prometheus.MustNewConstHistogram(k.hostResourcesUtilizedHist, counts[key], sums[key], hist, key)
	}
}
