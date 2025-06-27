// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package shared

import (
	"log/slog"

	"github.com/cobaltcore-dev/cortex/internal/extractor/plugins/shared"
	"github.com/cobaltcore-dev/cortex/internal/sync/openstack/nova"

	"github.com/cobaltcore-dev/cortex/internal/conf"
	"github.com/cobaltcore-dev/cortex/internal/db"
	"github.com/cobaltcore-dev/cortex/internal/kpis/plugins"
	"github.com/prometheus/client_golang/prometheus"
)

type HostTotalCapacityKPI struct {
	// Common base for all KPIs that provides standard functionality.
	plugins.BaseKPI[struct{}] // No options passed through yaml config

	hostTotalCapacityPerHost *prometheus.Desc
}

func (HostTotalCapacityKPI) GetName() string {
	return "host_total_capacity_kpi"
}

func (k *HostTotalCapacityKPI) Init(db db.DB, opts conf.RawOpts) error {
	if err := k.BaseKPI.Init(db, opts); err != nil {
		return err
	}
	k.hostTotalCapacityPerHost = prometheus.NewDesc(
		"cortex_host_total_capacity_per_host",
		"Total resources available on the hosts currently (individually by host).",
		[]string{"compute_host_name", "resource", "availability_zone"},
		nil,
	)
	return nil
}

func (k *HostTotalCapacityKPI) Describe(ch chan<- *prometheus.Desc) {
	ch <- k.hostTotalCapacityPerHost
}

func (k *HostTotalCapacityKPI) Collect(ch chan<- prometheus.Metric) {
	type HostTotalCapacityWithAvailabilityZone struct {
		shared.HostUtilization
		AvailabilityZone string `db:"availability_zone"`
	}

	var hostTotalCapacity []HostTotalCapacityWithAvailabilityZone

	aggregatesTableName := nova.Aggregate{}.TableName()
	hostUtilizationTableName := shared.HostUtilization{}.TableName()

	query := `
        SELECT
            f.*,
            a.availability_zone
        FROM ` + hostUtilizationTableName + ` AS f
        JOIN (
            SELECT DISTINCT compute_host, availability_zone
            FROM ` + aggregatesTableName + `
            WHERE availability_zone IS NOT NULL
        ) AS a
            ON f.compute_host = a.compute_host
    `

	if _, err := k.DB.Select(&hostTotalCapacity, query); err != nil {
		slog.Error("failed to select host utilization", "err", err)
		return
	}

	for _, hs := range hostTotalCapacity {

		ch <- prometheus.MustNewConstMetric(
			k.hostTotalCapacityPerHost,
			prometheus.GaugeValue,
			hs.TotalVCPUsAllocatable,
			hs.ComputeHost,
			"cpu",
			hs.AvailabilityZone,
		)
		ch <- prometheus.MustNewConstMetric(
			k.hostTotalCapacityPerHost,
			prometheus.GaugeValue,
			hs.TotalDiskAllocatableGB,
			hs.ComputeHost,
			"disk",
			hs.AvailabilityZone,
		)
		ch <- prometheus.MustNewConstMetric(
			k.hostTotalCapacityPerHost,
			prometheus.GaugeValue,
			hs.TotalMemoryAllocatableMB,
			hs.ComputeHost,
			"memory",
			hs.AvailabilityZone,
		)
	}

}
