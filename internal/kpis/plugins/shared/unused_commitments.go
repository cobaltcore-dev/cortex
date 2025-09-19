// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package shared

import (
	"log/slog"

	"github.com/cobaltcore-dev/cortex/internal/conf"
	"github.com/cobaltcore-dev/cortex/internal/db"
	"github.com/cobaltcore-dev/cortex/internal/extractor/plugins/shared"
	"github.com/cobaltcore-dev/cortex/internal/kpis/plugins"
	"github.com/prometheus/client_golang/prometheus"
)

// Currently does not include disk in the unused commitments metric.
type UnusedCommitments struct {
	AvailabilityZone string  `db:"availability_zone"`
	VCPUsUnused      float64 `db:"vcpus_unused"`
	RAMUnusedMB      float64 `db:"ram_unused_mb"`
}

type UnusedCommitmentsKPI struct {
	// Common base for all KPIs that provides standard functionality.
	plugins.BaseKPI[struct{}] // No options passed through yaml config

	unusedCommitmentsSum *prometheus.Desc
}

func (UnusedCommitmentsKPI) GetName() string {
	return "unused_commitments_kpi"
}

func (k *UnusedCommitmentsKPI) Init(db db.DB, opts conf.RawOpts) error {
	if err := k.BaseKPI.Init(db, opts); err != nil {
		return err
	}
	k.unusedCommitmentsSum = prometheus.NewDesc(
		"cortex_project_commitments_unused",
		"Current amount of unused commitments over all projects per availability_zone.",
		[]string{
			"resource",
			"availability_zone",
		},
		nil,
	)
	return nil
}

func (k *UnusedCommitmentsKPI) Describe(ch chan<- *prometheus.Desc) {
	ch <- k.unusedCommitmentsSum
}

func (k *UnusedCommitmentsKPI) Collect(ch chan<- prometheus.Metric) {
	projectCommitmentsTable := shared.ProjectResourceCommitments{}.TableName()
	projectUtilizationTable := shared.ProjectResourceUtilization{}.TableName()

	var commitmentsUnused []UnusedCommitments

	// First select all availability zones from the host_az table to ensure we have all AZs.
	// Then query all the commitments and resource usage per project and AZ.
	// Then sum them up per AZ, calculating the unused commitments per AZ.
	// Assume 0 unused if usage is higher than commitments.

	// In theory, if the host_az table is empty, we would not get any results.
	// Or if there are AZs in the commitments table that are not in the host_az table, we would miss them.
	// But both cases should not happen in practice.
	// The host_az table should always have all AZs.
	query := `
        SELECT
            azs.availability_zone,
            COALESCE(SUM(
                CASE
                    WHEN (comm.total_committed_vcpus - COALESCE(util.total_vcpus_used, 0)) < 0 THEN 0
                    ELSE (comm.total_committed_vcpus - COALESCE(util.total_vcpus_used, 0))
                END
            ), 0) AS vcpus_unused,
            COALESCE(SUM(
                CASE
                    WHEN (comm.total_committed_ram_mb - COALESCE(util.total_ram_used_mb, 0)) < 0 THEN 0
                    ELSE (comm.total_committed_ram_mb - COALESCE(util.total_ram_used_mb, 0))
                END
            ), 0) AS ram_unused_mb
        FROM (
            SELECT DISTINCT availability_zone FROM feature_host_az
        ) azs
        LEFT JOIN ` + projectCommitmentsTable + ` AS comm
            ON comm.availability_zone = azs.availability_zone
        LEFT JOIN ` + projectUtilizationTable + ` AS util
            ON comm.project_id = util.project_id AND comm.availability_zone = util.availability_zone
        GROUP BY azs.availability_zone
        ORDER BY azs.availability_zone
    `
	if _, err := k.DB.Select(&commitmentsUnused, query); err != nil {
		slog.Error("failed to select project commitments unused", "err", err)
		return
	}

	for _, c := range commitmentsUnused {
		ch <- prometheus.MustNewConstMetric(
			k.unusedCommitmentsSum,
			prometheus.GaugeValue,
			c.VCPUsUnused,
			"cpu",
			c.AvailabilityZone,
		)
		ch <- prometheus.MustNewConstMetric(
			k.unusedCommitmentsSum,
			prometheus.GaugeValue,
			c.RAMUnusedMB,
			"ram",
			c.AvailabilityZone,
		)
	}

}
