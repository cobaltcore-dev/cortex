// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package storage

import (
	"context"
	"log/slog"

	"github.com/cobaltcore-dev/cortex/api/v1alpha1"
	"github.com/cobaltcore-dev/cortex/internal/knowledge/db"
	"github.com/cobaltcore-dev/cortex/internal/knowledge/extractor/plugins/storage"
	"github.com/cobaltcore-dev/cortex/internal/knowledge/kpis/plugins"
	"github.com/cobaltcore-dev/cortex/pkg/conf"
	"github.com/cobaltcore-dev/cortex/pkg/tools"
	"github.com/prometheus/client_golang/prometheus"
	"sigs.k8s.io/controller-runtime/pkg/client"
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

func (k *NetAppStoragePoolCPUUsageKPI) Init(db *db.DB, client client.Client, opts conf.RawOpts) error {
	if err := k.BaseKPI.Init(db, client, opts); err != nil {
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
	knowledge := &v1alpha1.Knowledge{}
	if err := k.Client.Get(
		context.Background(),
		client.ObjectKey{Name: "netapp-storage-pool-cpu-usage-manila"},
		knowledge,
	); err != nil {
		slog.Error("failed to get knowledge netapp-storage-pool-cpu-usage", "err", err)
		return
	}
	usages, err := v1alpha1.
		UnboxFeatureList[storage.StoragePoolCPUUsage](knowledge.Status.Raw)
	if err != nil {
		slog.Error("failed to unbox storage pool cpu usage", "err", err)
		return
	}
	buckets := prometheus.LinearBuckets(0, 5, 20)
	keysFunc := func(usage storage.StoragePoolCPUUsage) []string {
		return []string{"cpu_usage_max"}
	}
	valueFunc := func(usage storage.StoragePoolCPUUsage) float64 {
		return usage.MaxCPUUsagePct
	}
	hists, counts, sums := tools.Histogram(usages, buckets, keysFunc, valueFunc)
	for key, hist := range hists {
		ch <- prometheus.MustNewConstHistogram(k.storagePoolCPUUsageMax, counts[key], sums[key], hist)
	}
	keysFunc = func(usage storage.StoragePoolCPUUsage) []string {
		return []string{"cpu_usage_avg"}
	}
	valueFunc = func(usage storage.StoragePoolCPUUsage) float64 {
		return float64(usage.AvgCPUUsagePct)
	}
	hists, counts, sums = tools.Histogram(usages, buckets, keysFunc, valueFunc)
	for key, hist := range hists {
		ch <- prometheus.MustNewConstHistogram(k.storagePoolCPUUsageAvg, counts[key], sums[key], hist)
	}
}
