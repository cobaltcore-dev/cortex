// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package shared

import (
	"log/slog"
	"strconv"

	"github.com/cobaltcore-dev/cortex/knowledge/api/features/shared"
	"github.com/cobaltcore-dev/cortex/knowledge/internal/kpis/plugins"
	"github.com/cobaltcore-dev/cortex/pkg/conf"
	"github.com/cobaltcore-dev/cortex/pkg/db"
	"github.com/prometheus/client_golang/prometheus"
)

// Advanced statistics about vm life spans.
type VMLifeSpanKPI struct {
	// Common base for all KPIs that provides standard functionality.
	plugins.BaseKPI[struct{}] // No options passed through yaml config

	// Time a vm was alive before it was deleted.
	lifeSpanDesc *prometheus.Desc
}

func (VMLifeSpanKPI) GetName() string {
	return "vm_life_span_kpi"
}

func (k *VMLifeSpanKPI) Init(db db.DB, opts conf.RawOpts) error {
	if err := k.BaseKPI.Init(db, opts); err != nil {
		return err
	}
	k.lifeSpanDesc = prometheus.NewDesc(
		"cortex_vm_life_span",
		"Time a VM was alive before it was deleted",
		[]string{"flavor_name", "deleted"},
		nil,
	)
	return nil
}

func (k *VMLifeSpanKPI) Describe(ch chan<- *prometheus.Desc) {
	ch <- k.lifeSpanDesc
}

func (k *VMLifeSpanKPI) Collect(ch chan<- prometheus.Metric) {
	// Process both deleted and running VMs in one loop
	for _, deleted := range []bool{true, false} {
		k.collectVMBuckets(ch, deleted)
	}
}

func (k *VMLifeSpanKPI) collectVMBuckets(ch chan<- prometheus.Metric, deleted bool) {
	var buckets []shared.VMLifeSpanHistogramBucket
	tableName := shared.VMLifeSpanHistogramBucket{}.TableName()

	if _, err := k.DB.Select(&buckets, "SELECT * FROM "+tableName+" WHERE deleted = $1", deleted); err != nil {
		slog.Error("failed to select vm life spans", "err", err, "deleted", deleted)
		return
	}

	bucketsByFlavor := make(map[string][]shared.VMLifeSpanHistogramBucket)
	for _, bucket := range buckets {
		if bucket.FlavorName == "" {
			slog.Warn("vm_life_span: empty flavor name in bucket", "bucket", bucket)
			continue
		}
		bucketsByFlavor[bucket.FlavorName] = append(bucketsByFlavor[bucket.FlavorName], bucket)
	}

	for flavor, flavorBuckets := range bucketsByFlavor {
		if len(flavorBuckets) == 0 {
			slog.Warn("vm_life_span: no buckets for flavor", "flavor", flavor)
			continue
		}

		var count uint64
		var sum float64
		hist := make(map[float64]uint64, len(flavorBuckets))

		for _, bucket := range flavorBuckets {
			hist[bucket.Bucket] = bucket.Value
			count = bucket.Count
			sum = bucket.Sum
		}

		ch <- prometheus.MustNewConstHistogram(
			k.lifeSpanDesc,
			count, sum, hist, flavor, strconv.FormatBool(deleted),
		)
	}
}
