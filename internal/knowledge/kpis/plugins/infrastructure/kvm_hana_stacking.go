// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package infrastructure

import (
	"context"
	"strings"

	"github.com/cobaltcore-dev/cortex/internal/knowledge/kpis/plugins"
	"github.com/cobaltcore-dev/cortex/pkg/conf"
	"github.com/cobaltcore-dev/cortex/pkg/knowledge/datasources/openstack/identity"
	"github.com/cobaltcore-dev/cortex/pkg/knowledge/datasources/openstack/nova"
	hv1 "github.com/cobaltcore-dev/openstack-hypervisor-operator/api/v1"
	"github.com/prometheus/client_golang/prometheus"
	"sigs.k8s.io/controller-runtime/pkg/client"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
)

var log = logf.Log.WithName("infrastructure")

const hanaKVMFlavorPrefix = "hana_k_"

type kvmHanaStackingRow struct {
	ProjectID   string
	ProjectName string
	DomainID    string
	DomainName  string
	ComputeHost string
	TotalRAMMB  float64
}

// Inputs of the kvm hana stacking kpi.
type KVMHanaStackingInputs struct {
	Servers  []nova.Server      `input:"servers"`
	Flavors  []nova.Flavor      `input:"flavors"`
	Projects []identity.Project `input:"projects"`
	Domains  []identity.Domain  `input:"domains"`
}

type KVMHanaStackingKPI struct {
	plugins.BaseKPI[struct{}]
	ramPerProjectAndHost *prometheus.Desc
}

func (k *KVMHanaStackingKPI) GetName() string {
	return "kvm_hana_stacking_kpi"
}

func (k *KVMHanaStackingKPI) Init(inputs plugins.Inputs, c client.Client, opts conf.RawOpts) error {
	if err := k.BaseKPI.Init(inputs, c, opts); err != nil {
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
	var in KVMHanaStackingInputs
	if err := k.ResolveInputs(context.Background(), &in); err != nil {
		log.Error(err, "kvm_hana_stacking: failed to resolve inputs")
		return
	}
	hosts, err := k.getKVMHosts()
	if err != nil {
		log.Error(err, "kvm_hana_stacking: failed to get KVM hosts")
		return
	}

	rows := hanaStacking(in)

	for _, row := range rows {
		host, ok := hosts[row.ComputeHost]
		if !ok {
			log.V(1).Info("kvm_hana_stacking: compute host not found", "compute_host", row.ComputeHost)
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

// Sum up the memory of active HANA instances on KVM hosts per project and
// compute host. Instances with unknown flavors are counted without memory.
func hanaStacking(in KVMHanaStackingInputs) []kvmHanaStackingRow {
	projects := projectDetailsByID(in.Projects, in.Domains)
	flavorsByName := make(map[string]nova.Flavor, len(in.Flavors))
	for _, f := range in.Flavors {
		flavorsByName[f.Name] = f
	}
	type key struct{ projectID, computeHost string }
	ramByKey := make(map[key]float64)
	for _, s := range in.Servers {
		if !isActiveServer(s) || !isKVMComputeHost(s.OSEXTSRVATTRHost) ||
			!strings.HasPrefix(s.FlavorName, hanaKVMFlavorPrefix) {
			continue
		}
		k := key{s.TenantID, s.OSEXTSRVATTRHost}
		ramByKey[k] += float64(flavorsByName[s.FlavorName].RAM)
	}
	rows := make([]kvmHanaStackingRow, 0, len(ramByKey))
	for k, ram := range ramByKey {
		p := projects[k.projectID]
		rows = append(rows, kvmHanaStackingRow{
			ProjectID:   k.projectID,
			ProjectName: p.ProjectName,
			DomainID:    p.DomainID,
			DomainName:  p.DomainName,
			ComputeHost: k.computeHost,
			TotalRAMMB:  ram,
		})
	}
	return rows
}

func init() {
	plugins.Index["kvm_hana_stacking_kpi"] = func() plugins.KPI { return &KVMHanaStackingKPI{} }
}
