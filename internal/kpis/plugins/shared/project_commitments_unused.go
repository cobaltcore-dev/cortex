// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package shared

import (
	"github.com/cobaltcore-dev/cortex/internal/conf"
	"github.com/cobaltcore-dev/cortex/internal/db"
	"github.com/cobaltcore-dev/cortex/internal/kpis/plugins"
	"github.com/prometheus/client_golang/prometheus"
)

type ProjectCommitmentsUnused struct {
	ProjectID    string  `db:"project_id"`
	VCPUsUnused  float64 `db:"vcpus_unused"`
	RAMUnusedMB  float64 `db:"ram_unused_mb"`
	DiskUnusedGB float64 `db:"disk_unused_gb"`
}

type ProjectCommitmentsUnusedKPI struct {
	// Common base for all KPIs that provides standard functionality.
	plugins.BaseKPI[struct{}] // No options passed through yaml config

	projectCommitmentsUnused *prometheus.Desc
}

func (ProjectCommitmentsUnusedKPI) GetName() string {
	return "project_commitments_unused_kpi"
}

func (k *ProjectCommitmentsUnusedKPI) Init(db db.DB, opts conf.RawOpts) error {
	if err := k.BaseKPI.Init(db, opts); err != nil {
		return err
	}
	k.projectCommitmentsUnused = prometheus.NewDesc(
		"cortex_project_commitments_unused",
		"Current amount of unused project commitments.",
		[]string{
			"project_id",
			"resource",
			"availability_zone",
		},
		nil,
	)
	return nil
}

func (k *ProjectCommitmentsUnusedKPI) Describe(ch chan<- *prometheus.Desc) {
	ch <- k.projectCommitmentsUnused
}

func (k *ProjectCommitmentsUnusedKPI) Collect(ch chan<- prometheus.Metric) {

}
