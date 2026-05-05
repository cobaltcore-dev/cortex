// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package infrastructure

import (
	"context"
	"log/slog"

	"github.com/cobaltcore-dev/cortex/api/v1alpha1"
	"github.com/cobaltcore-dev/cortex/internal/knowledge/db"
	"github.com/cobaltcore-dev/cortex/internal/knowledge/extractor/plugins/compute"
	"github.com/cobaltcore-dev/cortex/internal/knowledge/kpis/plugins"
	"github.com/cobaltcore-dev/cortex/pkg/conf"
	"github.com/prometheus/client_golang/prometheus"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

type VMwareHostCapacityKPI struct {
	plugins.BaseKPI[struct{}]

	capacityUsagePerHost *prometheus.Desc
	capacityTotalPerHost *prometheus.Desc
}

func (k *VMwareHostCapacityKPI) GetName() string {
	return "vmware_host_capacity_kpi"
}

func (k *VMwareHostCapacityKPI) Init(dbConn *db.DB, c client.Client, opts conf.RawOpts) error {
	if err := k.BaseKPI.Init(dbConn, c, opts); err != nil {
		return err
	}
	k.capacityUsagePerHost = prometheus.NewDesc(
		"cortex_vmware_host_capacity_usage",
		"Capacity usage per VMware host. CPU in vCPUs, memory and disk in bytes.",
		append(vmwareHostLabels, "resource"), nil,
	)
	k.capacityTotalPerHost = prometheus.NewDesc(
		"cortex_vmware_host_capacity_total",
		"Total allocatable capacity per VMware host. CPU in vCPUs, memory and disk in bytes.",
		append(vmwareHostLabels, "resource"), nil,
	)
	return nil
}

func (k *VMwareHostCapacityKPI) Describe(ch chan<- *prometheus.Desc) {
	ch <- k.capacityUsagePerHost
	ch <- k.capacityTotalPerHost
}

func (k *VMwareHostCapacityKPI) Collect(ch chan<- prometheus.Metric) {
	hosts, err := k.getVMwareHosts()
	if err != nil {
		slog.Error("vmware_host_capacity: failed to get vmware hosts", "error", err)
		return
	}
	utilizations, err := k.getHostUtilizations()
	if err != nil {
		slog.Error("vmware_host_capacity: failed to get host utilizations", "error", err)
		return
	}
	for _, host := range hosts {
		util, ok := utilizations[host.ComputeHost]
		if !ok {
			slog.Warn("vmware_host_capacity: missing utilization for host", "compute_host", host.ComputeHost)
			continue
		}

		labels := host.getHostLabels()

		ch <- prometheus.MustNewConstMetric(k.capacityUsagePerHost, prometheus.GaugeValue, util.VCPUsUsed, append(labels, "cpu")...)
		ch <- prometheus.MustNewConstMetric(k.capacityUsagePerHost, prometheus.GaugeValue, util.RAMUsedMB*1024*1024, append(labels, "ram")...)
		ch <- prometheus.MustNewConstMetric(k.capacityUsagePerHost, prometheus.GaugeValue, util.DiskUsedGB*1024*1024*1024, append(labels, "disk")...)

		ch <- prometheus.MustNewConstMetric(k.capacityTotalPerHost, prometheus.GaugeValue, util.TotalVCPUsAllocatable, append(labels, "cpu")...)
		ch <- prometheus.MustNewConstMetric(k.capacityTotalPerHost, prometheus.GaugeValue, util.TotalRAMAllocatableMB*1024*1024, append(labels, "ram")...)
		ch <- prometheus.MustNewConstMetric(k.capacityTotalPerHost, prometheus.GaugeValue, util.TotalDiskAllocatableGB*1024*1024*1024, append(labels, "disk")...)
	}
}

func (k *VMwareHostCapacityKPI) getVMwareHosts() ([]vmwareHost, error) {
	knowledge := &v1alpha1.Knowledge{}
	if err := k.Client.Get(context.Background(), client.ObjectKey{Name: hostDetailsKnowledgeName}, knowledge); err != nil {
		return nil, err
	}
	details, err := v1alpha1.UnboxFeatureList[compute.HostDetails](knowledge.Status.Raw)
	if err != nil {
		return nil, err
	}
	hosts := make([]vmwareHost, 0, len(details))
	for _, d := range details {
		if d.HypervisorType == vmwareIronicHypervisorType || d.HypervisorFamily != hypervisorFamilyVMware {
			continue
		}
		hosts = append(hosts, vmwareHost{HostDetails: d})
	}
	return hosts, nil
}

func (k *VMwareHostCapacityKPI) getHostUtilizations() (map[string]compute.HostUtilization, error) {
	knowledge := &v1alpha1.Knowledge{}
	if err := k.Client.Get(context.Background(), client.ObjectKey{Name: hostUtilizationKnowledgeName}, knowledge); err != nil {
		return nil, err
	}
	utils, err := v1alpha1.UnboxFeatureList[compute.HostUtilization](knowledge.Status.Raw)
	if err != nil {
		return nil, err
	}
	m := make(map[string]compute.HostUtilization, len(utils))
	for _, u := range utils {
		if u.TotalVCPUsAllocatable == 0 || u.TotalRAMAllocatableMB == 0 || u.TotalDiskAllocatableGB == 0 {
			slog.Warn("vmware_host_capacity: skipping host with zero allocatable resources", "compute_host", u.ComputeHost)
			continue
		}
		m[u.ComputeHost] = u
	}
	return m, nil
}
