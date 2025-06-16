// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package shared

import (
	"log/slog"

	"github.com/cobaltcore-dev/cortex/internal/extractor/plugins/shared"

	"github.com/cobaltcore-dev/cortex/internal/conf"
	"github.com/cobaltcore-dev/cortex/internal/db"
	"github.com/cobaltcore-dev/cortex/internal/kpis/plugins"
	"github.com/cobaltcore-dev/cortex/internal/tools"
	"github.com/prometheus/client_golang/prometheus"
)

type HostCapacityKPI struct {
	// Common base for all KPIs that provides standard functionality.
	plugins.BaseKPI[struct{}] // No options passed through yaml config

	hostResourcesUtilizedPerHost *prometheus.Desc
	hostResourcesUtilizedHist    *prometheus.Desc
}

func (HostCapacityKPI) GetName() string {
	return "host_utilization_kpi"
}

func (k *HostCapacityKPI) Init(db db.DB, opts conf.RawOpts) error {
	if err := k.BaseKPI.Init(db, opts); err != nil {
		return err
	}
	k.hostResourcesUtilizedPerHost = prometheus.NewDesc(
		"cortex_host_utilization_per_host_pct",
		"Resources utilized on the hosts currently (individually by host).",
		[]string{"compute_host_name", "resource"},
		nil,
	)
	k.hostResourcesUtilizedHist = prometheus.NewDesc(
		"cortex_host_utilization_pct",
		"Resources utilized on the hosts currently (aggregated as a histogram).",
		[]string{"resource"},
		nil,
	)
	return nil
}

func (k *HostCapacityKPI) Describe(ch chan<- *prometheus.Desc) {
	ch <- k.hostResourcesUtilizedPerHost
	ch <- k.hostResourcesUtilizedHist
}

func (k *HostCapacityKPI) Collect(ch chan<- prometheus.Metric) {
	var hostUtilization []shared.HostUtilization
	tableName := shared.HostUtilization{}.TableName()
	if _, err := k.DB.Select(&hostUtilization, "SELECT * FROM "+tableName); err != nil {
		slog.Error("failed to select host spaces", "err", err)
		return
	}
	for _, hs := range hostUtilization {
		ch <- prometheus.MustNewConstMetric(
			k.hostResourcesUtilizedPerHost,
			prometheus.GaugeValue,
			hs.VCPUsUtilizedPct,
			hs.ComputeHost,
			"cpu",
		)
		ch <- prometheus.MustNewConstMetric(
			k.hostResourcesUtilizedPerHost,
			prometheus.GaugeValue,
			hs.RAMUtilizedPct,
			hs.ComputeHost,
			"memory",
		)
		ch <- prometheus.MustNewConstMetric(
			k.hostResourcesUtilizedPerHost,
			prometheus.GaugeValue,
			hs.DiskUtilizedPct,
			hs.ComputeHost,
			"disk",
		)
	}
	buckets := prometheus.LinearBuckets(0, 5, 20)
	// Histogram for CPU
	keysFunc := func(hs shared.HostUtilization) []string { return []string{"cpu"} }
	valueFunc := func(hs shared.HostUtilization) float64 { return hs.VCPUsUtilizedPct }
	hists, counts, sums := tools.Histogram(hostUtilization, buckets, keysFunc, valueFunc)
	for key, hist := range hists {
		ch <- prometheus.MustNewConstHistogram(k.hostResourcesUtilizedHist, counts[key], sums[key], hist, key)
	}
	// Histogram for Memory
	keysFunc = func(hs shared.HostUtilization) []string { return []string{"memory"} }
	valueFunc = func(hs shared.HostUtilization) float64 { return hs.RAMUtilizedPct }
	hists, counts, sums = tools.Histogram(hostUtilization, buckets, keysFunc, valueFunc)
	for key, hist := range hists {
		ch <- prometheus.MustNewConstHistogram(k.hostResourcesUtilizedHist, counts[key], sums[key], hist, key)
	}
	// Histogram for Disk
	keysFunc = func(hs shared.HostUtilization) []string { return []string{"disk"} }
	valueFunc = func(hs shared.HostUtilization) float64 { return hs.DiskUtilizedPct }
	hists, counts, sums = tools.Histogram(hostUtilization, buckets, keysFunc, valueFunc)
	for key, hist := range hists {
		ch <- prometheus.MustNewConstHistogram(k.hostResourcesUtilizedHist, counts[key], sums[key], hist, key)
	}
}
