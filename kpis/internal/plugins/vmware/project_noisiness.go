// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package vmware

import (
	"log/slog"

	"github.com/cobaltcore-dev/cortex/extractor/api/features/vmware"
	"github.com/cobaltcore-dev/cortex/kpis/internal/plugins"
	"github.com/cobaltcore-dev/cortex/lib/conf"
	"github.com/cobaltcore-dev/cortex/lib/db"
	"github.com/cobaltcore-dev/cortex/lib/tools"
	"github.com/prometheus/client_golang/prometheus"
)

type VMwareProjectNoisinessKPI struct {
	// Common base for all KPIs that provides standard functionality.
	plugins.BaseKPI[struct{}] // No options passed through yaml config

	projectNoisinessDesc *prometheus.Desc
}

func (VMwareProjectNoisinessKPI) GetName() string {
	return "vmware_project_noisiness_kpi"
}

func (k *VMwareProjectNoisinessKPI) Init(db db.DB, opts conf.RawOpts) error {
	if err := k.BaseKPI.Init(db, opts); err != nil {
		return err
	}
	k.projectNoisinessDesc = prometheus.NewDesc(
		"cortex_vmware_project_noisiness",
		"Project noisiness of vROps projects over the configured prometheus sync period.",
		nil, nil,
	)
	return nil
}

func (k *VMwareProjectNoisinessKPI) Describe(ch chan<- *prometheus.Desc) {
	ch <- k.projectNoisinessDesc
}

func (k *VMwareProjectNoisinessKPI) Collect(ch chan<- prometheus.Metric) {
	var features []vmware.VROpsProjectNoisiness
	tableName := vmware.VROpsProjectNoisiness{}.TableName()
	if _, err := k.DB.Select(&features, "SELECT * FROM "+tableName); err != nil {
		slog.Error("failed to select project noisiness", "err", err)
		return
	}
	buckets := prometheus.LinearBuckets(0, 5, 20)
	keysFunc := func(noisiness vmware.VROpsProjectNoisiness) []string {
		return []string{"project_noisiness"}
	}
	valueFunc := func(noisiness vmware.VROpsProjectNoisiness) float64 {
		return float64(noisiness.AvgCPUOfProject)
	}
	hists, counts, sums := tools.Histogram(features, buckets, keysFunc, valueFunc)
	for key, hist := range hists {
		ch <- prometheus.MustNewConstHistogram(k.projectNoisinessDesc, counts[key], sums[key], hist)
	}
}
