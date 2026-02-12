// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package compute

import (
	"context"
	"log/slog"

	"github.com/cobaltcore-dev/cortex/api/v1alpha1"
	"github.com/cobaltcore-dev/cortex/internal/knowledge/db"
	"github.com/cobaltcore-dev/cortex/internal/knowledge/extractor/plugins/compute"
	"github.com/cobaltcore-dev/cortex/internal/knowledge/kpis/plugins"
	"github.com/cobaltcore-dev/cortex/pkg/conf"
	"github.com/cobaltcore-dev/cortex/pkg/tools"
	"github.com/prometheus/client_golang/prometheus"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

type VMwareProjectNoisinessKPI struct {
	// Common base for all KPIs that provides standard functionality.
	plugins.BaseKPI[struct{}] // No options passed through yaml config

	projectNoisinessDesc *prometheus.Desc
}

func (VMwareProjectNoisinessKPI) GetName() string {
	return "vmware_project_noisiness_kpi"
}

func (k *VMwareProjectNoisinessKPI) Init(db *db.DB, client client.Client, opts conf.RawOpts) error {
	if err := k.BaseKPI.Init(db, client, opts); err != nil {
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
	knowledge := &v1alpha1.Knowledge{}
	if err := k.Client.Get(
		context.Background(),
		client.ObjectKey{Name: "vmware-project-noisiness"},
		knowledge,
	); err != nil {
		slog.Error("failed to get knowledge vmware-project-noisiness", "err", err)
		return
	}
	features, err := v1alpha1.
		UnboxFeatureList[compute.VROpsProjectNoisiness](knowledge.Status.Raw)
	if err != nil {
		slog.Error("failed to unbox vmware project noisiness", "err", err)
		return
	}
	buckets := prometheus.LinearBuckets(0, 5, 20)
	keysFunc := func(noisiness compute.VROpsProjectNoisiness) []string {
		return []string{"project_noisiness"}
	}
	valueFunc := func(noisiness compute.VROpsProjectNoisiness) float64 {
		return float64(noisiness.AvgCPUOfProject)
	}
	hists, counts, sums := tools.Histogram(features, buckets, keysFunc, valueFunc)
	for key, hist := range hists {
		ch <- prometheus.MustNewConstHistogram(k.projectNoisinessDesc, counts[key], sums[key], hist)
	}
}
