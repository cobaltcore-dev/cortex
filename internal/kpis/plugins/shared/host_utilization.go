// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package shared

import (
	"log/slog"
	"strconv"

	"github.com/cobaltcore-dev/cortex/internal/conf"
	"github.com/cobaltcore-dev/cortex/internal/db"
	"github.com/cobaltcore-dev/cortex/internal/kpis/plugins"
	"github.com/cobaltcore-dev/cortex/internal/sync/openstack"
	"github.com/prometheus/client_golang/prometheus"
)

type HostUtilizationKPI struct {
	// Common base for all KPIs that provides standard functionality.
	plugins.BaseKPI[struct{}] // No options passed through yaml config

	hostResourceUsedPerHost *prometheus.Desc
	hostResourceUsedHist    *prometheus.Desc
}

func (HostUtilizationKPI) GetName() string {
	return "host_utilization_kpi"
}

func (k *HostUtilizationKPI) Init(db db.DB, opts conf.RawOpts) error {
	if err := k.BaseKPI.Init(db, opts); err != nil {
		return err
	}
	k.hostResourceUsedPerHost = prometheus.NewDesc(
		"cortex_host_utilization_per_host_pct",
		"Resources used on the hosts currently (individually by host).",
		[]string{"hypervisor_id", "compute_host_name", "hypervisor_host_name", "resource"},
		nil,
	)
	k.hostResourceUsedHist = prometheus.NewDesc(
		"cortex_host_utilization_pct",
		"Resources used on the hosts currently (aggregated as a histogram).",
		[]string{"resource"},
		nil,
	)
	return nil
}

func (k *HostUtilizationKPI) Describe(ch chan<- *prometheus.Desc) {
	ch <- k.hostResourceUsedPerHost
	ch <- k.hostResourceUsedHist
}

func (k *HostUtilizationKPI) Collect(ch chan<- prometheus.Metric) {
	var hypervisors []openstack.Hypervisor
	tableName := openstack.Hypervisor{}.TableName()
	if _, err := k.DB.Select(&hypervisors, "SELECT * FROM "+tableName); err != nil {
		slog.Error("failed to select hypervisors", "err", err)
		return
	}
	for _, hypervisor := range hypervisors {
		if hypervisor.VCPUs != 0 {
			ch <- prometheus.MustNewConstMetric(
				k.hostResourceUsedPerHost,
				prometheus.GaugeValue,
				float64(hypervisor.VCPUsUsed)/float64(hypervisor.VCPUs),
				strconv.Itoa(hypervisor.ID),
				hypervisor.ServiceHost,
				hypervisor.Hostname,
				"cpu",
			)
		}
		if hypervisor.MemoryMB != 0 {
			ch <- prometheus.MustNewConstMetric(
				k.hostResourceUsedPerHost,
				prometheus.GaugeValue,
				float64(hypervisor.MemoryMBUsed)/float64(hypervisor.MemoryMB),
				strconv.Itoa(hypervisor.ID),
				hypervisor.ServiceHost,
				hypervisor.Hostname,
				"memory",
			)
		}
		if hypervisor.LocalGB != 0 {
			ch <- prometheus.MustNewConstMetric(
				k.hostResourceUsedPerHost,
				prometheus.GaugeValue,
				float64(hypervisor.LocalGBUsed)/float64(hypervisor.LocalGB),
				strconv.Itoa(hypervisor.ID),
				hypervisor.ServiceHost,
				hypervisor.Hostname,
				"disk",
			)
		}
	}
	buckets := prometheus.LinearBuckets(0, 5, 20)
	keysFunc := func(hypervisor openstack.Hypervisor) []string {
		return []string{"cpu"}
	}
	valueFunc := func(hypervisor openstack.Hypervisor) float64 {
		if hypervisor.VCPUs == 0 {
			return 0
		}
		return 100 * float64(hypervisor.VCPUsUsed) / float64(hypervisor.VCPUs)
	}
	hists, counts, sums := plugins.Histogram(hypervisors, buckets, keysFunc, valueFunc)
	for key, hist := range hists {
		ch <- prometheus.MustNewConstHistogram(k.hostResourceUsedHist, counts[key], sums[key], hist, key)
	}
	keysFunc = func(hypervisor openstack.Hypervisor) []string {
		return []string{"memory"}
	}
	valueFunc = func(hypervisor openstack.Hypervisor) float64 {
		if hypervisor.MemoryMB == 0 {
			return 0
		}
		return 100 * float64(hypervisor.MemoryMBUsed) / float64(hypervisor.MemoryMB)
	}
	hists, counts, sums = plugins.Histogram(hypervisors, buckets, keysFunc, valueFunc)
	for key, hist := range hists {
		ch <- prometheus.MustNewConstHistogram(k.hostResourceUsedHist, counts[key], sums[key], hist, key)
	}
	keysFunc = func(hypervisor openstack.Hypervisor) []string {
		return []string{"disk"}
	}
	valueFunc = func(hypervisor openstack.Hypervisor) float64 {
		if hypervisor.LocalGB == 0 {
			return 0
		}
		return 100 * float64(hypervisor.LocalGBUsed) / float64(hypervisor.LocalGB)
	}
	hists, counts, sums = plugins.Histogram(hypervisors, buckets, keysFunc, valueFunc)
	for key, hist := range hists {
		ch <- prometheus.MustNewConstHistogram(k.hostResourceUsedHist, counts[key], sums[key], hist, key)
	}
}
