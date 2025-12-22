// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package compute

import (
	"context"
	"log/slog"

	"github.com/cobaltcore-dev/cortex/api/v1alpha1"
	"github.com/cobaltcore-dev/cortex/internal/knowledge/extractor/plugins/compute"
	"github.com/cobaltcore-dev/cortex/internal/knowledge/kpis/plugins"
	"github.com/cobaltcore-dev/cortex/pkg/conf"
	"github.com/cobaltcore-dev/cortex/pkg/db"
	"github.com/cobaltcore-dev/cortex/pkg/tools"
	"github.com/prometheus/client_golang/prometheus"
	"sigs.k8s.io/controller-runtime/pkg/client"
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

func (k *VMwareHostContentionKPI) Init(db *db.DB, client client.Client, opts conf.RawOpts) error {
	if err := k.BaseKPI.Init(db, client, opts); err != nil {
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
	knowledge := &v1alpha1.Knowledge{}
	if err := k.Client.Get(
		context.Background(),
		client.ObjectKey{Name: "vmware-long-term-contended-hosts"},
		knowledge,
	); err != nil {
		slog.Error("failed to get knowledge vmware-long-term-contended-hosts", "err", err)
		return
	}
	contentions, err := v1alpha1.
		UnboxFeatureList[compute.VROpsHostsystemContentionLongTerm](knowledge.Status.Raw)
	if err != nil {
		slog.Error("failed to unbox vmware hostsystem contention", "err", err)
		return
	}
	buckets := prometheus.LinearBuckets(0, 5, 20)
	keysFunc := func(contention compute.VROpsHostsystemContentionLongTerm) []string {
		return []string{"cpu_contention_max"}
	}
	valueFunc := func(contention compute.VROpsHostsystemContentionLongTerm) float64 {
		return contention.MaxCPUContention
	}
	hists, counts, sums := tools.Histogram(contentions, buckets, keysFunc, valueFunc)
	for key, hist := range hists {
		ch <- prometheus.MustNewConstHistogram(k.hostCPUContentionMax, counts[key], sums[key], hist)
	}
	keysFunc = func(contention compute.VROpsHostsystemContentionLongTerm) []string {
		return []string{"cpu_contention_avg"}
	}
	valueFunc = func(contention compute.VROpsHostsystemContentionLongTerm) float64 {
		return float64(contention.AvgCPUContention)
	}
	hists, counts, sums = tools.Histogram(contentions, buckets, keysFunc, valueFunc)
	for key, hist := range hists {
		ch <- prometheus.MustNewConstHistogram(k.hostCPUContentionAvg, counts[key], sums[key], hist)
	}
}
