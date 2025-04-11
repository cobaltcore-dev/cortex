// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package shared

import (
	"github.com/cobaltcore-dev/cortex/internal/conf"
	"github.com/cobaltcore-dev/cortex/internal/db"
	"github.com/cobaltcore-dev/cortex/internal/features/plugins/shared"
	"github.com/cobaltcore-dev/cortex/internal/kpis/plugins"
	"github.com/cobaltcore-dev/cortex/internal/monitoring"
	"github.com/prometheus/client_golang/prometheus"
)

// Advanced statistics about vm life spans.
type VMLifeSpanKPI struct {
	// Common base for all KPIs that provides standard functionality.
	plugins.BaseKPI[struct{}] // No options passed through yaml config

	// Time a vm was alive before it was deleted.
	lifeSpan *prometheus.HistogramVec
}

func (VMLifeSpanKPI) GetName() string {
	return "vm_life_span_kpi"
}

func (k *VMLifeSpanKPI) Init(db db.DB, opts conf.RawOpts, r *monitoring.Registry) error {
	if err := k.BaseKPI.Init(db, opts, r); err != nil {
		return err
	}
	k.lifeSpan = prometheus.NewHistogramVec(prometheus.HistogramOpts{
		Name:    "cortex_vm_life_span",
		Help:    "Time a VM was alive before it was deleted",
		Buckets: prometheus.ExponentialBucketsRange(5, 365*24*60*60, 30),
	}, []string{"flavor_name", "flavor_id"})
	k.Registry.MustRegister(
		k.lifeSpan,
	)
	return nil
}

func (k *VMLifeSpanKPI) Update() error {
	var vmLifeSpans []shared.VMLifeSpan
	tableName := shared.VMLifeSpan{}.TableName()
	if _, err := k.DB.Select(&vmLifeSpans, "SELECT * FROM "+tableName); err != nil {
		return err
	}
	k.lifeSpan.Reset()
	for _, lifeSpan := range vmLifeSpans {
		k.lifeSpan.WithLabelValues(
			lifeSpan.FlavorName,
			lifeSpan.FlavorID,
		).Observe(float64(lifeSpan.Duration))
		k.lifeSpan.WithLabelValues(
			"all",
			"all",
		).Observe(float64(lifeSpan.Duration))
	}
	return nil
}
