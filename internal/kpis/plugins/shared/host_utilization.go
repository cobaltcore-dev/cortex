// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package shared

import (
	"log/slog"

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
		[]string{"compute_host_name", "resource", "availability_zone"},
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

func (k *HostUtilizationKPI) Describe(ch chan<- *prometheus.Desc) {
	ch <- k.hostResourcesUtilizedPerHost
	ch <- k.hostResourcesUtilizedHist
}

func (k *HostUtilizationKPI) Collect(ch chan<- prometheus.Metric) {
	type HostUtilizationPerAvailabilityZone struct {
		shared.HostUtilization
		AvailabilityZone string `db:"availability_zone"`
	}

	var hostUtilization []HostUtilizationPerAvailabilityZone

	aggregatesTableName := nova.Aggregate{}.TableName()
	hostUtilizationTableName := shared.HostUtilization{}.TableName()

	query := `
		SELECT * FROM ` + hostUtilizationTableName + ` AS f
		JOIN (
    		SELECT DISTINCT compute_host, availability_zone
    		FROM ` + aggregatesTableName + `
    		WHERE availability_zone IS NOT NULL
		) AS a
    	ON f.compute_host = a.compute_host;
	`

	if _, err := k.DB.Select(&hostUtilization, query); err != nil {
		slog.Error("failed to select host utilization", "err", err)
		return
	}

	for _, hs := range hostUtilization {
		ch <- prometheus.MustNewConstMetric(
			k.hostResourcesUtilizedPerHost,
			prometheus.GaugeValue,
			hs.VCPUsUtilizedPct,
			hs.ComputeHost,
			"cpu",
			hs.AvailabilityZone,
		)
		ch <- prometheus.MustNewConstMetric(
			k.hostResourcesUtilizedPerHost,
			prometheus.GaugeValue,
			hs.RAMUtilizedPct,
			hs.ComputeHost,
			"memory",
			hs.AvailabilityZone,
		)
		ch <- prometheus.MustNewConstMetric(
			k.hostResourcesUtilizedPerHost,
			prometheus.GaugeValue,
			hs.DiskUtilizedPct,
			hs.ComputeHost,
			"disk",
			hs.AvailabilityZone,
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
