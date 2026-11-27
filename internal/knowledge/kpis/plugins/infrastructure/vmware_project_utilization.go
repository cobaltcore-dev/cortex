// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package infrastructure

import (
	"context"

	"github.com/cobaltcore-dev/cortex/internal/knowledge/extractor/plugins/compute"
	"github.com/cobaltcore-dev/cortex/internal/knowledge/kpis/plugins"
	"github.com/cobaltcore-dev/cortex/pkg/conf"
	"github.com/cobaltcore-dev/cortex/pkg/knowledge/datasources/openstack/identity"
	"github.com/cobaltcore-dev/cortex/pkg/knowledge/datasources/openstack/nova"
	"github.com/prometheus/client_golang/prometheus"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

type vmwareProjectInstanceCount = projectInstanceCount

type vmwareProjectCapacityUsage = projectCapacityUsage

// Inputs of the vmware project utilization kpi.
type VMwareProjectUtilizationInputs struct {
	Servers  []nova.Server      `input:"servers"`
	Flavors  []nova.Flavor      `input:"flavors"`
	Projects []identity.Project `input:"projects"`
	Domains  []identity.Domain  `input:"domains"`
	// Features of the vmware host details knowledge.
	HostDetails []compute.VMwareHostDetails `input:"hostDetails"`
}

type VMwareProjectUtilizationKPI struct {
	// BaseKPI provides common fields and methods for all KPIs, such as database connection and Kubernetes client.
	plugins.BaseKPI[struct{}]

	// instanceCountPerProjectAndHostAndFlavor is a Prometheus descriptor for the number of running instances per project, hypervisor, and flavor on VMware.
	instanceCountPerProjectAndHostAndFlavor *prometheus.Desc

	// capacityUsagePerProjectAndHost is a Prometheus descriptor for the resource capacity used by a project per VMware hypervisor and flavor. CPU in vCPUs, memory and disk in bytes.
	capacityUsagePerProjectAndHost *prometheus.Desc
}

func (k *VMwareProjectUtilizationKPI) GetName() string {
	return "vmware_project_utilization_kpi"
}

func (k *VMwareProjectUtilizationKPI) Init(inputs plugins.Inputs, c client.Client, opts conf.RawOpts) error {
	if err := k.BaseKPI.Init(inputs, c, opts); err != nil {
		return err
	}

	k.instanceCountPerProjectAndHostAndFlavor = prometheus.NewDesc(
		"cortex_vmware_project_instances",
		"Number of running instances per project, hypervisor, and flavor on VMware.",
		append(vmwareHostLabels, "project_id", "project_name", "domain_id", "domain_name", "flavor_name"), nil,
	)
	k.capacityUsagePerProjectAndHost = prometheus.NewDesc(
		"cortex_vmware_project_capacity_usage",
		"Resource capacity used by a project per VMware hypervisor and flavor. CPU in vCPUs, memory and disk in bytes.",
		append(vmwareHostLabels, "project_id", "project_name", "domain_id", "domain_name", "resource"), nil,
	)
	return nil
}

func (k *VMwareProjectUtilizationKPI) Describe(ch chan<- *prometheus.Desc) {
	ch <- k.instanceCountPerProjectAndHostAndFlavor
	ch <- k.capacityUsagePerProjectAndHost
}

func (k *VMwareProjectUtilizationKPI) Collect(ch chan<- prometheus.Metric) {
	var in VMwareProjectUtilizationInputs
	if err := k.ResolveInputs(context.Background(), &in); err != nil {
		// Log the error and return early. The KPI will be retried on the next scrape.
		log.Error(err, "vmware_project_utilization: failed to resolve inputs")
		return
	}
	hosts := make(map[string]vmwareHost, len(in.HostDetails))
	for _, host := range in.HostDetails {
		hosts[host.ComputeHost] = vmwareHost{VMwareHostDetails: host}
	}

	// Export project x flavor x compute_host instance count metric
	projects := projectDetailsByID(in.Projects, in.Domains)
	projectInstanceCounts := projectInstanceCounts(in.Servers, projects, isVMwareComputeHost)
	for _, projectInstanceCount := range projectInstanceCounts {
		host, ok := hosts[projectInstanceCount.ComputeHost]
		if !ok {
			log.V(1).Info("vmware_project_utilization: Compute host not found for project instance count", "compute_host", projectInstanceCount.ComputeHost)
			continue
		}
		hostLabels := host.getHostLabels()
		hostLabels = append(hostLabels, projectInstanceCount.ProjectID, projectInstanceCount.ProjectName, projectInstanceCount.DomainID, projectInstanceCount.DomainName, projectInstanceCount.FlavorName)
		ch <- prometheus.MustNewConstMetric(k.instanceCountPerProjectAndHostAndFlavor, prometheus.GaugeValue, projectInstanceCount.InstanceCount, hostLabels...)
	}

	// Export project x compute_host x resource capacity usage metric
	projectCapacityUsages := projectCapacityUsages(in.Servers, in.Flavors, projects, isVMwareComputeHost)
	for _, projectCapacityUsage := range projectCapacityUsages {
		host, ok := hosts[projectCapacityUsage.ComputeHost]
		if !ok {
			log.V(1).Info("vmware_project_utilization: Compute host not found for project capacity usage", "compute_host", projectCapacityUsage.ComputeHost)
			continue
		}
		hostLabels := host.getHostLabels()
		hostLabels = append(hostLabels, projectCapacityUsage.ProjectID, projectCapacityUsage.ProjectName, projectCapacityUsage.DomainID, projectCapacityUsage.DomainName)

		ch <- prometheus.MustNewConstMetric(k.capacityUsagePerProjectAndHost, prometheus.GaugeValue, projectCapacityUsage.TotalVCPUs, append(hostLabels, "cpu")...)
		ch <- prometheus.MustNewConstMetric(k.capacityUsagePerProjectAndHost, prometheus.GaugeValue, projectCapacityUsage.TotalRAMMB*1024*1024, append(hostLabels, "ram")...)
		ch <- prometheus.MustNewConstMetric(k.capacityUsagePerProjectAndHost, prometheus.GaugeValue, projectCapacityUsage.TotalDiskGB*1024*1024*1024, append(hostLabels, "disk")...)
	}
}

func init() {
	plugins.Index["vmware_project_utilization_kpi"] = func() plugins.KPI { return &VMwareProjectUtilizationKPI{} }
}
