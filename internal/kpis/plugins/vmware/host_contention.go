// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package vmware

import (
	"log/slog"

	"github.com/cobaltcore-dev/cortex/internal/conf"
	"github.com/cobaltcore-dev/cortex/internal/db"
	"github.com/cobaltcore-dev/cortex/internal/features/plugins/vmware"
	"github.com/cobaltcore-dev/cortex/internal/kpis/plugins"
	"github.com/prometheus/client_golang/prometheus"
)

type VMwareHostContentionKPI struct {
	// Common base for all KPIs that provides standard functionality.
	plugins.BaseKPI[struct{}] // No options passed through yaml config

	hostCPUContentionMax *prometheus.Desc
	hostCPUContentionAvg *prometheus.Desc
}

func (VMwareHostContentionKPI) GetName() string {
	return "vmware_host_contention_kpi"
}

func (k *VMwareHostContentionKPI) Init(db db.DB, opts conf.RawOpts) error {
	if err := k.BaseKPI.Init(db, opts); err != nil {
		return err
	}
	k.hostCPUContentionMax = prometheus.NewDesc(
		"cortex_vmware_host_cpu_contention_max",
		"Max CPU contention of vROps hostsystems over the configured prometheus sync period.",
		nil, nil,
	)
	k.hostCPUContentionAvg = prometheus.NewDesc(
		"cortex_vmware_host_cpu_contention_avg",
		"Avg CPU contention of vROps hostsystems over the configured prometheus sync period.",
		nil, nil,
	)
	return nil
}

func (k *VMwareHostContentionKPI) Describe(ch chan<- *prometheus.Desc) {
	ch <- k.hostCPUContentionMax
	ch <- k.hostCPUContentionAvg
}

func (k *VMwareHostContentionKPI) Collect(ch chan<- prometheus.Metric) {
	var contentions []vmware.VROpsHostsystemContention
	tableName := vmware.VROpsHostsystemContention{}.TableName()
	if _, err := k.DB.Select(&contentions, "SELECT * FROM "+tableName); err != nil {
		slog.Error("failed to select hostsystem contention", "err", err)
		return
	}
	buckets := prometheus.LinearBuckets(0, 5, 20)
	keysFunc := func(contention vmware.VROpsHostsystemContention) []string {
		return []string{"cpu_contention_max"}
	}
	valueFunc := func(contention vmware.VROpsHostsystemContention) float64 {
		return contention.MaxCPUContention
	}
	hists, counts, sums := plugins.Histogram(contentions, buckets, keysFunc, valueFunc)
	for key, hist := range hists {
		ch <- prometheus.MustNewConstHistogram(k.hostCPUContentionMax, counts[key], sums[key], hist)
	}
	keysFunc = func(contention vmware.VROpsHostsystemContention) []string {
		return []string{"cpu_contention_avg"}
	}
	valueFunc = func(contention vmware.VROpsHostsystemContention) float64 {
		return float64(contention.AvgCPUContention)
	}
	hists, counts, sums = plugins.Histogram(contentions, buckets, keysFunc, valueFunc)
	for key, hist := range hists {
		ch <- prometheus.MustNewConstHistogram(k.hostCPUContentionAvg, counts[key], sums[key], hist)
	}
}
