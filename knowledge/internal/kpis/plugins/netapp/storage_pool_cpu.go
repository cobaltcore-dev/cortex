// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package netapp

import (
	"log/slog"

	"github.com/cobaltcore-dev/cortex/knowledge/api/features/netapp"
	"github.com/cobaltcore-dev/cortex/knowledge/internal/kpis/plugins"
	"github.com/cobaltcore-dev/cortex/pkg/conf"
	"github.com/cobaltcore-dev/cortex/pkg/db"
	"github.com/cobaltcore-dev/cortex/pkg/tools"
	"github.com/prometheus/client_golang/prometheus"
)

type NetAppStoragePoolCPUUsageKPI struct {
	// Common base for all KPIs that provides standard functionality.
	plugins.BaseKPI[struct{}] // No options passed through yaml config

	storagePoolCPUUsageMax *prometheus.Desc
	storagePoolCPUUsageAvg *prometheus.Desc
}

func (NetAppStoragePoolCPUUsageKPI) GetName() string {
	return "netapp_storage_pool_cpu_usage_kpi"
}

func (k *NetAppStoragePoolCPUUsageKPI) Init(db db.DB, opts conf.RawOpts) error {
	if err := k.BaseKPI.Init(db, opts); err != nil {
		return err
	}
	k.storagePoolCPUUsageMax = prometheus.NewDesc(
		"cortex_netapp_storage_pool_cpu_usage_max",
		"Max CPU usage of NetApp storage pools over the configured prometheus sync period.",
		nil, nil,
	)
	k.storagePoolCPUUsageAvg = prometheus.NewDesc(
		"cortex_netapp_storage_pool_cpu_usage_avg",
		"Avg CPU usage of NetApp storage pools over the configured prometheus sync period.",
		nil, nil,
	)
	return nil
}

func (k *NetAppStoragePoolCPUUsageKPI) Describe(ch chan<- *prometheus.Desc) {
	ch <- k.storagePoolCPUUsageMax
	ch <- k.storagePoolCPUUsageAvg
}

func (k *NetAppStoragePoolCPUUsageKPI) Collect(ch chan<- prometheus.Metric) {
	var usages []netapp.StoragePoolCPUUsage
	tableName := netapp.StoragePoolCPUUsage{}.TableName()
	if _, err := k.DB.Select(&usages, "SELECT * FROM "+tableName); err != nil {
		slog.Error("failed to select storage pool CPU usage", "err", err)
		return
	}
	buckets := prometheus.LinearBuckets(0, 5, 20)
	keysFunc := func(usage netapp.StoragePoolCPUUsage) []string {
		return []string{"cpu_usage_max"}
	}
	valueFunc := func(usage netapp.StoragePoolCPUUsage) float64 {
		return usage.MaxCPUUsagePct
	}
	hists, counts, sums := tools.Histogram(usages, buckets, keysFunc, valueFunc)
	for key, hist := range hists {
		ch <- prometheus.MustNewConstHistogram(k.storagePoolCPUUsageMax, counts[key], sums[key], hist)
	}
	keysFunc = func(usage netapp.StoragePoolCPUUsage) []string {
		return []string{"cpu_usage_avg"}
	}
	valueFunc = func(usage netapp.StoragePoolCPUUsage) float64 {
		return float64(usage.AvgCPUUsagePct)
	}
	hists, counts, sums = tools.Histogram(usages, buckets, keysFunc, valueFunc)
	for key, hist := range hists {
		ch <- prometheus.MustNewConstHistogram(k.storagePoolCPUUsageAvg, counts[key], sums[key], hist)
	}
}
