// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package shared

import (
	"log/slog"
	"strings"

	"github.com/cobaltcore-dev/cortex/internal/conf"
	"github.com/cobaltcore-dev/cortex/internal/db"
	"github.com/cobaltcore-dev/cortex/internal/extractor/plugins/shared"
	"github.com/cobaltcore-dev/cortex/internal/kpis/plugins"
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
		[]string{"flavor_name"},
		nil,
	)
	return nil
}

func (k *VMLifeSpanKPI) Describe(ch chan<- *prometheus.Desc) {
	ch <- k.lifeSpanDesc
}

func (k *VMLifeSpanKPI) Collect(ch chan<- prometheus.Metric) {
	var vmLifeSpans []shared.VMLifeSpan
	tableName := shared.VMLifeSpan{}.TableName()
	if _, err := k.DB.Select(&vmLifeSpans, "SELECT * FROM "+tableName); err != nil {
		slog.Error("failed to select vm life spans", "err", err)
		return
	}
	buckets := prometheus.ExponentialBucketsRange(5, 365*24*60*60, 30)
	keysFunc := func(lifeSpan shared.VMLifeSpan) []string {
		return []string{lifeSpan.FlavorName, "all"}
	}
	valueFunc := func(lifeSpan shared.VMLifeSpan) float64 {
		return float64(lifeSpan.Duration)
	}
	hists, counts, sums := plugins.Histogram(vmLifeSpans, buckets, keysFunc, valueFunc)
	for key, hist := range hists {
		labels := strings.Split(key, ",")
		if len(labels) != 1 {
			slog.Warn("vm_life_span: unexpected comma in flavor name")
			continue
		}
		ch <- prometheus.MustNewConstHistogram(k.lifeSpanDesc, counts[key], sums[key], hist, labels...)
	}
}
