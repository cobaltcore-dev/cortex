// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package infrastructure

import (
	"context"
	"log/slog"

	"github.com/cobaltcore-dev/cortex/internal/knowledge/datasources/plugins/openstack/identity"
	"github.com/cobaltcore-dev/cortex/internal/knowledge/datasources/plugins/openstack/nova"
	"github.com/cobaltcore-dev/cortex/internal/knowledge/db"
	"github.com/cobaltcore-dev/cortex/internal/knowledge/kpis/plugins"
	"github.com/cobaltcore-dev/cortex/pkg/conf"
	hv1 "github.com/cobaltcore-dev/openstack-hypervisor-operator/api/v1"
	"github.com/prometheus/client_golang/prometheus"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

const hanaKVMFlavorPattern = "hana_k_%"

type kvmHanaStackingRow struct {
	ProjectID   string  `db:"project_id"`
	ProjectName string  `db:"project_name"`
	DomainID    string  `db:"domain_id"`
	DomainName  string  `db:"domain_name"`
	ComputeHost string  `db:"compute_host"`
	TotalRAMMB  float64 `db:"total_ram_mb"`
}

type KVMHanaStackingKPI struct {
	plugins.BaseKPI[struct{}]
	ramPerProjectAndHost *prometheus.Desc
}

func (k *KVMHanaStackingKPI) GetName() string {
	return "kvm_hana_stacking_kpi"
}

func (k *KVMHanaStackingKPI) Init(dbConn *db.DB, c client.Client, opts conf.RawOpts) error {
	if err := k.BaseKPI.Init(dbConn, c, opts); err != nil {
		return err
	}
	k.ramPerProjectAndHost = prometheus.NewDesc(
		"cortex_kvm_hana_stacking_ram_bytes",
		"Total RAM in bytes used by HANA instances of a project on a KVM hypervisor.",
		append(kvmHostLabels, "project_id", "project_name", "domain_id", "domain_name"), nil,
	)
	return nil
}

func (k *KVMHanaStackingKPI) Describe(ch chan<- *prometheus.Desc) {
	ch <- k.ramPerProjectAndHost
}

func (k *KVMHanaStackingKPI) Collect(ch chan<- prometheus.Metric) {
	hosts, err := k.getKVMHosts()
	if err != nil {
		slog.Error("kvm_hana_stacking: failed to get KVM hosts", "error", err)
		return
	}

	rows, err := k.queryHanaStacking()
	if err != nil {
		slog.Error("kvm_hana_stacking: failed to query HANA stacking", "error", err)
		return
	}

	for _, row := range rows {
		host, ok := hosts[row.ComputeHost]
		if !ok {
			slog.Warn("kvm_hana_stacking: compute host not found", "compute_host", row.ComputeHost)
			continue
		}
		hostLabels := host.getHostLabels()
		hostLabels = append(hostLabels, row.ProjectID, row.ProjectName, row.DomainID, row.DomainName)
		ch <- prometheus.MustNewConstMetric(k.ramPerProjectAndHost, prometheus.GaugeValue, row.TotalRAMMB*1024*1024, hostLabels...)
	}
}

func (k *KVMHanaStackingKPI) getKVMHosts() (map[string]kvmHost, error) {
	hvs := &hv1.HypervisorList{}
	if err := k.Client.List(context.Background(), hvs); err != nil {
		return nil, err
	}
	hosts := make(map[string]kvmHost, len(hvs.Items))
	for _, hv := range hvs.Items {
		host := kvmHost{Hypervisor: hv}
		hosts[host.Name] = host
	}
	return hosts, nil
}

func (k *KVMHanaStackingKPI) queryHanaStacking() ([]kvmHanaStackingRow, error) {
	query := `
		SELECT
			s.tenant_id AS project_id,
			COALESCE(p.name, '') AS project_name,
			COALESCE(p.domain_id, '') AS domain_id,
			COALESCE(d.name, '') AS domain_name,
			s.os_ext_srv_attr_host AS compute_host,
			COALESCE(SUM(f.ram), 0) AS total_ram_mb
		FROM ` + nova.Server{}.TableName() + ` s
		LEFT JOIN ` + nova.Flavor{}.TableName() + ` f ON s.flavor_name = f.name
		LEFT JOIN ` + identity.Project{}.TableName() + ` p ON p.id = s.tenant_id
		LEFT JOIN ` + identity.Domain{}.TableName() + ` d ON d.id = p.domain_id
		WHERE s.status NOT IN ('DELETED', 'ERROR')
		  AND s.os_ext_srv_attr_host LIKE '` + kvmComputeHostPattern + `'
		  AND s.flavor_name LIKE '` + hanaKVMFlavorPattern + `'
		GROUP BY s.tenant_id, p.name, p.domain_id, d.name, s.os_ext_srv_attr_host
	`
	var rows []kvmHanaStackingRow
	if _, err := k.DB.Select(&rows, query); err != nil {
		return nil, err
	}
	return rows, nil
}
