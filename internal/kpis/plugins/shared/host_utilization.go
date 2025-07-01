// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package shared

import (
	"encoding/json"
	"log/slog"
	"strconv"

	"github.com/cobaltcore-dev/cortex/internal/extractor/plugins/shared"
	"github.com/cobaltcore-dev/cortex/internal/sync/openstack/nova"

	"github.com/cobaltcore-dev/cortex/internal/conf"
	"github.com/cobaltcore-dev/cortex/internal/db"
	"github.com/cobaltcore-dev/cortex/internal/kpis/plugins"
	"github.com/cobaltcore-dev/cortex/internal/tools"
	"github.com/prometheus/client_golang/prometheus"
)

type HostUtilizationKPI struct {
	// Common base for all KPIs that provides standard functionality.
	plugins.BaseKPI[struct{}] // No options passed through yaml config

	hostResourcesUtilizedPerHost *prometheus.Desc
	hostResourcesUtilizedHist    *prometheus.Desc
	hostTotalCapacityPerHost     *prometheus.Desc
}

func (HostUtilizationKPI) GetName() string {
	return "host_utilization_kpi"
}

func (k *HostUtilizationKPI) Init(db db.DB, opts conf.RawOpts) error {
	if err := k.BaseKPI.Init(db, opts); err != nil {
		return err
	}
	k.hostResourcesUtilizedPerHost = prometheus.NewDesc(
		"cortex_host_utilization_per_host_pct",
		"Resources utilized on the hosts currently (individually by host).",
		[]string{"compute_host_name", "resource", "availability_zone", "cpu_model", "total", "running_vms", "traits"},
		nil,
	)
	k.hostResourcesUtilizedHist = prometheus.NewDesc(
		"cortex_host_utilization_pct",
		"Resources utilized on the hosts currently (aggregated as a histogram).",
		[]string{"resource"},
		nil,
	)
	k.hostTotalCapacityPerHost = prometheus.NewDesc(
		"cortex_total_capacity_per_host",
		"Total resources available on the hosts currently (individually by host).",
		[]string{"compute_host_name", "resource", "availability_zone", "cpu_model", "traits"},
		nil,
	)
	return nil
}

func (k *HostUtilizationKPI) Describe(ch chan<- *prometheus.Desc) {
	ch <- k.hostResourcesUtilizedPerHost
	ch <- k.hostResourcesUtilizedHist
	ch <- k.hostTotalCapacityPerHost
}

func (k *HostUtilizationKPI) Collect(ch chan<- prometheus.Metric) {
	type HostUtilizationPerAvailabilityZone struct {
		shared.HostUtilization
		AvailabilityZone string `db:"availability_zone"`
		CPUInfo          string `db:"cpu_info"`    // Hypervisor CPU info
		RunningVMs       int    `db:"running_vms"` // Number of running VMs on the host
		Traits           string `db:"traits"`      // Traits of the host
	}

	var hostUtilization []HostUtilizationPerAvailabilityZone

	aggregatesTableName := nova.Aggregate{}.TableName()
	hostUtilizationTableName := shared.HostUtilization{}.TableName()
	hostCapabilitiesTableName := shared.HostCapabilities{}.TableName()
	hypervisorsTableName := nova.Hypervisor{}.TableName()

	query := `
		SELECT
			f.*,
			a.availability_zone,
			h.cpu_info,
			h.running_vms,
			fhc.traits
		FROM ` + hostUtilizationTableName + ` AS f
		JOIN (
			SELECT DISTINCT compute_host, availability_zone
			FROM ` + aggregatesTableName + `
			WHERE availability_zone IS NOT NULL
		) AS a
			ON f.compute_host = a.compute_host
		JOIN ` + hypervisorsTableName + ` AS h
			ON f.compute_host = h.service_host
		LEFT JOIN ` + hostCapabilitiesTableName + ` AS fhc
			ON f.compute_host = fhc.compute_host;
    `

	if _, err := k.DB.Select(&hostUtilization, query); err != nil {
		slog.Error("failed to select host utilization", "err", err)
		return
	}

	type CPUInfo struct {
		Model *string `json:"model,omitempty"`
	}

	for _, hs := range hostUtilization {
		var cpuInfo CPUInfo
		cpuModel := ""

		if hs.CPUInfo != "" {
			err := json.Unmarshal([]byte(hs.CPUInfo), &cpuInfo)
			// Get the CPU model from the CPU info if available.
			// If the CPU info is not available or the model is not set, use an empty string.
			if err == nil && cpuInfo.Model != nil {
				cpuModel = *cpuInfo.Model
			}
		}

		ch <- prometheus.MustNewConstMetric(
			k.hostResourcesUtilizedPerHost,
			prometheus.GaugeValue,
			hs.VCPUsUtilizedPct,
			hs.ComputeHost,
			"cpu",
			hs.AvailabilityZone,
			cpuModel,
			strconv.FormatFloat(hs.TotalVCPUsAllocatable, 'f', 0, 64),
			strconv.Itoa(hs.RunningVMs),
			hs.Traits,
		)
		ch <- prometheus.MustNewConstMetric(
			k.hostResourcesUtilizedPerHost,
			prometheus.GaugeValue,
			hs.RAMUtilizedPct,
			hs.ComputeHost,
			"memory",
			hs.AvailabilityZone,
			cpuModel,
			strconv.FormatFloat(hs.TotalMemoryAllocatableMB, 'f', -1, 64),
			strconv.Itoa(hs.RunningVMs),
			hs.Traits,
		)
		ch <- prometheus.MustNewConstMetric(
			k.hostResourcesUtilizedPerHost,
			prometheus.GaugeValue,
			hs.DiskUtilizedPct,
			hs.ComputeHost,
			"disk",
			hs.AvailabilityZone,
			cpuModel,
			strconv.FormatFloat(hs.TotalDiskAllocatableGB, 'f', -1, 64),
			strconv.Itoa(hs.RunningVMs),
			hs.Traits,
		)

		ch <- prometheus.MustNewConstMetric(
			k.hostTotalCapacityPerHost,
			prometheus.GaugeValue,
			hs.TotalVCPUsAllocatable,
			hs.ComputeHost,
			"cpu",
			hs.AvailabilityZone,
			cpuModel,
			hs.Traits,
		)
		ch <- prometheus.MustNewConstMetric(
			k.hostTotalCapacityPerHost,
			prometheus.GaugeValue,
			hs.TotalDiskAllocatableGB,
			hs.ComputeHost,
			"disk",
			hs.AvailabilityZone,
			cpuModel,
			hs.Traits,
		)
		ch <- prometheus.MustNewConstMetric(
			k.hostTotalCapacityPerHost,
			prometheus.GaugeValue,
			hs.TotalMemoryAllocatableMB,
			hs.ComputeHost,
			"memory",
			hs.AvailabilityZone,
			cpuModel,
			hs.Traits,
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
