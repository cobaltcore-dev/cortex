// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package shared

import (
	"fmt"
	"log/slog"
	"strings"

	"github.com/cobaltcore-dev/cortex/internal/conf"
	"github.com/cobaltcore-dev/cortex/internal/db"
	"github.com/cobaltcore-dev/cortex/internal/extractor/plugins/shared"
	"github.com/cobaltcore-dev/cortex/internal/kpis/plugins"
	"github.com/cobaltcore-dev/cortex/internal/tools"
	"github.com/prometheus/client_golang/prometheus"
)

// Advanced statistics about openstack migrations.
type VMMigrationStatisticsKPI struct {
	// Common base for all KPIs that provides standard functionality.
	plugins.BaseKPI[struct{}] // No options passed through yaml config

	// Time a VM has been on a host before migration.
	timeUntilMigrationDesc *prometheus.Desc
	// Number of migrations.
	nMigrations *prometheus.GaugeVec
}

func (VMMigrationStatisticsKPI) GetName() string {
	return "vm_migration_statistics_kpi"
}

func (k *VMMigrationStatisticsKPI) Init(db db.DB, opts conf.RawOpts) error {
	if err := k.BaseKPI.Init(db, opts); err != nil {
		return err
	}
	k.timeUntilMigrationDesc = prometheus.NewDesc(
		"cortex_vm_time_until_migration",
		"Time a VM has been on a host before migration",
		[]string{"type", "flavor_name"},
		nil,
	)
	k.nMigrations = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "cortex_migrations_total",
		Help: "Number of migrations",
	}, []string{"type", "source_host", "target_host", "source_node", "target_node"})
	return nil
}

func (k *VMMigrationStatisticsKPI) Describe(ch chan<- *prometheus.Desc) {
	ch <- k.timeUntilMigrationDesc
	k.nMigrations.Describe(ch)
}

func (k *VMMigrationStatisticsKPI) Collect(ch chan<- prometheus.Metric) {
	slog.Info("collecting vm migration statistics")
	defer slog.Info("finished collecting vm migration statistics")

	var hostResidencies []shared.VMHostResidency
	tableName := shared.VMHostResidency{}.TableName()
	if _, err := k.DB.Select(&hostResidencies, "SELECT * FROM "+tableName); err != nil {
		slog.Error("failed to select vm host residencies", "err", err)
		return
	}
	buckets := prometheus.ExponentialBucketsRange(5, 365*24*60*60, 30)
	keysFunc := func(residency shared.VMHostResidency) []string {
		return []string{
			residency.Type + "," + residency.FlavorName,
			"all,all",
		}
	}
	valueFunc := func(residency shared.VMHostResidency) float64 {
		return float64(residency.Duration)
	}
	hists, counts, sums := tools.Histogram(hostResidencies, buckets, keysFunc, valueFunc)
	for key, hist := range hists {
		labels := strings.Split(key, ",")
		if len(labels) != 2 {
			slog.Warn("vm_migration_statistics: unexpected comma in migration type, flavor name or id")
			continue
		}
		ch <- prometheus.MustNewConstHistogram(k.timeUntilMigrationDesc, counts[key], sums[key], hist, labels...)
	}

	nMigrations := make(map[string]int)
	for _, r := range hostResidencies {
		key := fmt.Sprintf(
			"%s,%s,%s,%s,%s",
			r.Type, r.SourceHost, r.TargetHost, r.SourceNode, r.TargetNode,
		)
		nMigrations[key]++
	}
	for key, n := range nMigrations {
		parts := strings.Split(key, ",")
		k.nMigrations.WithLabelValues(
			parts[0], parts[1], parts[2], parts[3], parts[4],
		).Set(float64(n))
	}
	k.nMigrations.Collect(ch)
}
