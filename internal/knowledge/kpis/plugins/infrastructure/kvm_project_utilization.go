// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package infrastructure

import (
	"context"

	"github.com/cobaltcore-dev/cortex/internal/knowledge/kpis/plugins"
	"github.com/cobaltcore-dev/cortex/pkg/conf"
	"github.com/cobaltcore-dev/cortex/pkg/knowledge/datasources/openstack/identity"
	"github.com/cobaltcore-dev/cortex/pkg/knowledge/datasources/openstack/nova"
	hv1 "github.com/cobaltcore-dev/openstack-hypervisor-operator/api/v1"
	"github.com/prometheus/client_golang/prometheus"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

type kvmProjectInstanceCount = projectInstanceCount

type kvmProjectCapacityUsage = projectCapacityUsage

// Inputs of the kvm project utilization kpi.
type KVMProjectUtilizationInputs struct {
	Servers  []nova.Server      `input:"servers"`
	Flavors  []nova.Flavor      `input:"flavors"`
	Projects []identity.Project `input:"projects"`
	Domains  []identity.Domain  `input:"domains"`
}

type KVMProjectUtilizationKPI struct {
	// BaseKPI provides common fields and methods for all KPIs, such as database connection and Kubernetes client.
	plugins.BaseKPI[struct{}]

	// instanceCountPerProjectAndHostAndFlavor is a Prometheus descriptor for the metric that counts the number of instances per project, host, and flavor.
	instanceCountPerProjectAndHostAndFlavor *prometheus.Desc

	// capacityUsagePerProjectAndHost is a Prometheus descriptor for the metric that measures the capacity usage per project and host.
	capacityUsagePerProjectAndHost *prometheus.Desc
}

func (k *KVMProjectUtilizationKPI) GetName() string {
	return "kvm_project_utilization_kpi"
}

func (k *KVMProjectUtilizationKPI) Init(inputs plugins.Inputs, c client.Client, opts conf.RawOpts) error {
	if err := k.BaseKPI.Init(inputs, c, opts); err != nil {
		return err
	}

	k.instanceCountPerProjectAndHostAndFlavor = prometheus.NewDesc(
		"cortex_kvm_project_instances",
		"Number of running instances per project, hypervisor, and flavor on KVM.",
		append(kvmHostLabels, "project_id", "project_name", "domain_id", "domain_name", "flavor_name"), nil,
	)
	k.capacityUsagePerProjectAndHost = prometheus.NewDesc(
		"cortex_kvm_project_capacity_usage",
		"Resource capacity used by a project per KVM hypervisor and flavor. CPU in vCPUs, memory and disk in bytes.",
		append(kvmHostLabels, "project_id", "project_name", "domain_id", "domain_name", "resource"), nil,
	)
	return nil
}

func (k *KVMProjectUtilizationKPI) Describe(ch chan<- *prometheus.Desc) {
	ch <- k.instanceCountPerProjectAndHostAndFlavor
	ch <- k.capacityUsagePerProjectAndHost
}

func (k *KVMProjectUtilizationKPI) Collect(ch chan<- prometheus.Metric) {
	var in KVMProjectUtilizationInputs
	if err := k.ResolveInputs(context.Background(), &in); err != nil {
		log.Error(err, "kvm_project_utilization: failed to resolve inputs")
		return
	}
	hosts, err := k.getKVMHosts()
	if err != nil {
		log.Error(err, "kvm_project_utilization: failed to get KVM hosts")
		return
	}

	// Export project x flavor x compute_host instance count metric
	projects := projectDetailsByID(in.Projects, in.Domains)
	projectInstanceCounts := projectInstanceCounts(in.Servers, projects, isKVMComputeHost)
	for _, projectInstanceCount := range projectInstanceCounts {
		host, ok := hosts[projectInstanceCount.ComputeHost]
		if !ok {
			log.V(1).Info("kvm_project_utilization: Compute host not found for project instance count", "compute_host", projectInstanceCount.ComputeHost)
			continue
		}
		hostLabels := host.getHostLabels()
		hostLabels = append(hostLabels, projectInstanceCount.ProjectID, projectInstanceCount.ProjectName, projectInstanceCount.DomainID, projectInstanceCount.DomainName, projectInstanceCount.FlavorName)
		ch <- prometheus.MustNewConstMetric(k.instanceCountPerProjectAndHostAndFlavor, prometheus.GaugeValue, projectInstanceCount.InstanceCount, hostLabels...)
	}

	// Export project x compute_host x resource capacity usage metric
	projectCapacityUsages := projectCapacityUsages(in.Servers, in.Flavors, projects, isKVMComputeHost)
	for _, projectCapacityUsage := range projectCapacityUsages {
		host, ok := hosts[projectCapacityUsage.ComputeHost]
		if !ok {
			log.V(1).Info("kvm_project_utilization: Compute host not found for project capacity usage", "compute_host", projectCapacityUsage.ComputeHost)
			continue
		}
		hostLabels := host.getHostLabels()
		hostLabels = append(hostLabels, projectCapacityUsage.ProjectID, projectCapacityUsage.ProjectName, projectCapacityUsage.DomainID, projectCapacityUsage.DomainName)

		ch <- prometheus.MustNewConstMetric(k.capacityUsagePerProjectAndHost, prometheus.GaugeValue, projectCapacityUsage.TotalVCPUs, append(hostLabels, "cpu")...)
		ch <- prometheus.MustNewConstMetric(k.capacityUsagePerProjectAndHost, prometheus.GaugeValue, projectCapacityUsage.TotalRAMMB*1024*1024, append(hostLabels, "ram")...)
		ch <- prometheus.MustNewConstMetric(k.capacityUsagePerProjectAndHost, prometheus.GaugeValue, projectCapacityUsage.TotalDiskGB*1024*1024*1024, append(hostLabels, "disk")...)
	}
}

// getKVMHosts retrieves the list of KVM hosts and their details from the database, returning a map keyed by compute host name.
func (k *KVMProjectUtilizationKPI) getKVMHosts() (map[string]kvmHost, error) {
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

func init() {
	plugins.Index["kvm_project_utilization_kpi"] = func() plugins.KPI { return &KVMProjectUtilizationKPI{} }
}
