// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package sap

import (
	"log/slog"

	"github.com/cobaltcore-dev/cortex/internal/knowledge/datasources/openstack/nova"
	"github.com/cobaltcore-dev/cortex/internal/knowledge/kpis/plugins"
	"github.com/cobaltcore-dev/cortex/pkg/conf"
	"github.com/cobaltcore-dev/cortex/pkg/db"
	"github.com/prometheus/client_golang/prometheus"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

type FlavorRunningVMs struct {
	FlavorName       string  `db:"flavor_name"`
	AvailabilityZone string  `db:"availability_zone"`
	RunningVMs       float64 `db:"running_vms"`
}

type FlavorRunningVMsKPI struct {
	// Common base for all KPIs that provides standard functionality.
	plugins.BaseKPI[struct{}] // No options passed through yaml config
	flavorRunningVMs          *prometheus.Desc
}

func (FlavorRunningVMsKPI) GetName() string {
	return "sap_flavor_running_vms_kpi"
}

func (k *FlavorRunningVMsKPI) Init(db *db.DB, client client.Client, opts conf.RawOpts) error {
	if err := k.BaseKPI.Init(db, client, opts); err != nil {
		return err
	}
	k.flavorRunningVMs = prometheus.NewDesc(
		"cortex_sap_flavor_running_vms",
		"Current amount of running virtual machines per flavor.",
		[]string{
			"flavor_name",
			"availability_zone",
		},
		nil,
	)
	return nil
}

func (k *FlavorRunningVMsKPI) Describe(ch chan<- *prometheus.Desc) {
	ch <- k.flavorRunningVMs
}

func (k *FlavorRunningVMsKPI) Collect(ch chan<- prometheus.Metric) {
	var results []FlavorRunningVMs

	query := `
        SELECT
            flavor_name,
            COALESCE(os_ext_az_availability_zone, 'unknown') AS availability_zone,
            COUNT(*) AS running_vms
        FROM ` + nova.Server{}.TableName() + `
        WHERE
            status != 'DELETED'
        GROUP BY
            flavor_name,
            os_ext_az_availability_zone
        ORDER BY
            flavor_name;
    `

	if _, err := k.DB.Select(&results, query); err != nil {
		slog.Error("failed to select running vms per flavor", "err", err)
		return
	}
	slog.Info("flavor running vms results", "results", results)
	for _, r := range results {
		ch <- prometheus.MustNewConstMetric(
			k.flavorRunningVMs,
			prometheus.GaugeValue,
			r.RunningVMs,
			r.FlavorName,
			r.AvailabilityZone,
		)
	}
}
