// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package compute

import (
	"log/slog"

	"github.com/cobaltcore-dev/cortex/internal/knowledge/datasources/plugins/openstack/identity"
	"github.com/cobaltcore-dev/cortex/internal/knowledge/datasources/plugins/openstack/nova"
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
	ProjectID        string  `db:"project_id"`
	ProjectName      string  `db:"project_name"`
}

type FlavorRunningVMsKPI struct {
	// Common base for all KPIs that provides standard functionality.
	plugins.BaseKPI[struct{}] // No options passed through yaml config
	flavorRunningVMs          *prometheus.Desc
}

func (FlavorRunningVMsKPI) GetName() string {
	return "flavor_running_vms_kpi"
}

func (k *FlavorRunningVMsKPI) Init(db *db.DB, client client.Client, opts conf.RawOpts) error {
	if err := k.BaseKPI.Init(db, client, opts); err != nil {
		return err
	}
	k.flavorRunningVMs = prometheus.NewDesc(
		"cortex_flavor_running_vms",
		"Current amount of running virtual machines per flavor.",
		[]string{
			"flavor_name",
			"availability_zone",
			"project_id",
			"project_name",
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
			os.tenant_id AS project_id,
			p.name AS project_name,
            os.flavor_name,
            COALESCE(os.os_ext_az_availability_zone, 'unknown') AS availability_zone,
            COUNT(*) AS running_vms
        FROM ` + nova.Server{}.TableName() + ` os
		JOIN ` + identity.Project{}.TableName() + ` p ON p.id = os.tenant_id
        WHERE
            status != 'DELETED'
        GROUP BY
			os.tenant_id,
            p.name,
			os.flavor_name,
            os.os_ext_az_availability_zone
        ORDER BY
            os.tenant_id;
    `

	if _, err := k.DB.Select(&results, query); err != nil {
		slog.Error("failed to select running vms per flavor", "err", err)
		return
	}
	for _, r := range results {
		ch <- prometheus.MustNewConstMetric(
			k.flavorRunningVMs,
			prometheus.GaugeValue,
			r.RunningVMs,
			r.FlavorName,
			r.AvailabilityZone,
			r.ProjectID,
			r.ProjectName,
		)
	}
}
