// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package shared

import (
	"fmt"
	"strings"

	"github.com/cobaltcore-dev/cortex/internal/conf"
	"github.com/cobaltcore-dev/cortex/internal/db"
	"github.com/cobaltcore-dev/cortex/internal/features/plugins/shared"
	"github.com/cobaltcore-dev/cortex/internal/kpis/plugins"
	"github.com/cobaltcore-dev/cortex/internal/monitoring"
	"github.com/prometheus/client_golang/prometheus"
)

// Advanced statistics about openstack migrations.
type VMMigrationStatisticsKPI struct {
	// Common base for all KPIs that provides standard functionality.
	plugins.BaseKPI[struct{}] // No options passed through yaml config

	// Time a VM has been on a host before migration.
	timeUntilMigration *prometheus.HistogramVec
	// Number of migrations.
	nMigrations *prometheus.GaugeVec
}

func (VMMigrationStatisticsKPI) GetName() string {
	return "vm_migration_statistics_kpi"
}

func (k *VMMigrationStatisticsKPI) Init(db db.DB, opts conf.RawOpts, r *monitoring.Registry) error {
	if err := k.BaseKPI.Init(db, opts, r); err != nil {
		return err
	}
	k.timeUntilMigration = prometheus.NewHistogramVec(prometheus.HistogramOpts{
		Name:    "cortex_vm_time_until_migration", // + per flavor
		Help:    "Time a VM has been on a host before migration",
		Buckets: prometheus.ExponentialBucketsRange(5, 365*24*60*60, 30),
	}, []string{"type", "flavor_name", "flavor_id"})
	k.nMigrations = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "cortex_migrations_total",
		Help: "Number of migrations",
	}, []string{"type", "source_host", "target_host", "source_node", "target_node"})
	k.Registry.MustRegister(
		k.timeUntilMigration,
		k.nMigrations,
	)
	return nil
}

func (k *VMMigrationStatisticsKPI) Update() error {
	var hostResidencies []shared.VMHostResidency
	tableName := shared.VMHostResidency{}.TableName()
	if _, err := k.DB.Select(&hostResidencies, "SELECT * FROM "+tableName); err != nil {
		return err
	}
	k.timeUntilMigration.Reset()
	for _, residency := range hostResidencies {
		k.timeUntilMigration.WithLabelValues(
			residency.Type,
			residency.FlavorName,
			residency.FlavorID,
		).Observe(float64(residency.Duration))
		k.timeUntilMigration.WithLabelValues(
			residency.Type,
			"all",
			"all",
		).Observe(float64(residency.Duration))
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
	return nil
}
