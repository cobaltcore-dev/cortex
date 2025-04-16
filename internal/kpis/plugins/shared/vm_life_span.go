// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package shared

import (
	"log/slog"
	"strings"

	"github.com/cobaltcore-dev/cortex/internal/conf"
	"github.com/cobaltcore-dev/cortex/internal/db"
	"github.com/cobaltcore-dev/cortex/internal/features/plugins/shared"
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
		[]string{"flavor_name", "flavor_id"},
		nil,
	)
	return nil
}

func (k *VMLifeSpanKPI) Describe(ch chan<- *prometheus.Desc) {
	ch <- k.lifeSpanDesc
}

// Calculate the histogram of vm life spans.
func (k *VMLifeSpanKPI) histogram(vmLifeSpans []shared.VMLifeSpan) (
	hists map[string]map[float64]uint64,
	counts map[string]uint64,
	sums map[string]float64,
) {

	hists = map[string]map[float64]uint64{}
	counts = map[string]uint64{}
	sums = map[string]float64{}
	buckets := prometheus.ExponentialBucketsRange(5, 365*24*60*60, 30)
	for _, lifeSpan := range vmLifeSpans {
		keys := []string{lifeSpan.FlavorName + "," + lifeSpan.FlavorID, "all,all"}
		for _, key := range keys {
			if _, ok := hists[key]; !ok {
				hists[key] = make(map[float64]uint64, len(buckets))
			}
			for _, bucket := range buckets {
				if float64(lifeSpan.Duration) <= bucket {
					hists[key][bucket]++
				}
			}
			counts[key]++
			sums[key] += float64(lifeSpan.Duration)
		}
	}
	// Fill up empty buckets
	for key, hist := range hists {
		for _, bucket := range buckets {
			if _, ok := hist[bucket]; !ok {
				hists[key][bucket] = 0
			}
		}
	}
	return hists, counts, sums
}

func (k *VMLifeSpanKPI) Collect(ch chan<- prometheus.Metric) {
	var vmLifeSpans []shared.VMLifeSpan
	tableName := shared.VMLifeSpan{}.TableName()
	if _, err := k.DB.Select(&vmLifeSpans, "SELECT * FROM "+tableName); err != nil {
		slog.Error("failed to select vm life spans", "err", err)
		return
	}
	hists, counts, sums := k.histogram(vmLifeSpans)
	for key, hist := range hists {
		labels := strings.Split(key, ",")
		if len(labels) != 2 {
			slog.Warn("vm_life_span: unexpected comma in flavor name or id")
			continue
		}
		ch <- prometheus.MustNewConstHistogram(k.lifeSpanDesc, counts[key], sums[key], hist, labels...)
	}
}
