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

type kvmProjectInstanceCount struct {
	ProjectID        string  `db:"project_id"`
	ProjectName      string  `db:"project_name"`
	DomainID         string  `db:"domain_id"`
	DomainName       string  `db:"domain_name"`
	ComputeHost      string  `db:"compute_host"`
	FlavorName       string  `db:"flavor_name"`
	AvailabilityZone string  `db:"availability_zone"`
	InstanceCount    float64 `db:"instance_count"`
}

type kvmProjectCapacityUsage struct {
	ProjectID        string  `db:"project_id"`
	ProjectName      string  `db:"project_name"`
	DomainID         string  `db:"domain_id"`
	DomainName       string  `db:"domain_name"`
	ComputeHost      string  `db:"compute_host"`
	AvailabilityZone string  `db:"availability_zone"`
	TotalVCPUs       float64 `db:"total_vcpus"`
	TotalRAMMB       float64 `db:"total_ram_mb"`
	TotalDiskGB      float64 `db:"total_disk_gb"`
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

func (k *KVMProjectUtilizationKPI) Init(dbConn *db.DB, c client.Client, opts conf.RawOpts) error {
	if err := k.BaseKPI.Init(dbConn, c, opts); err != nil {
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
	hosts, err := k.getKVMHosts()
	if err != nil {
		slog.Error("kvm_project_utilization: failed to get KVM hosts", "error", err)
		return
	}

	// Export project x flavor x compute_host instance count metric
	projectInstanceCounts, err := k.queryProjectInstanceCount()
	if err != nil {
		slog.Error("kvm_project_utilization: Failed to query project instance count for project utilization KPI", "error", err)
		return
	}
	for _, projectInstanceCount := range projectInstanceCounts {
		host, ok := hosts[projectInstanceCount.ComputeHost]
		if !ok {
			slog.Warn("kvm_project_utilization: Compute host not found for project instance count", "compute_host", projectInstanceCount.ComputeHost)
			continue
		}
		hostLabels := host.getHostLabels()
		hostLabels = append(hostLabels, projectInstanceCount.ProjectID, projectInstanceCount.ProjectName, projectInstanceCount.DomainID, projectInstanceCount.DomainName, projectInstanceCount.FlavorName)
		ch <- prometheus.MustNewConstMetric(k.instanceCountPerProjectAndHostAndFlavor, prometheus.GaugeValue, projectInstanceCount.InstanceCount, hostLabels...)
	}

	// Export project x compute_host x resource capacity usage metric
	projectCapacityUsages, err := k.queryProjectCapacityUsage()
	if err != nil {
		slog.Error("kvm_project_utilization: Failed to query project capacity usage for project utilization KPI", "error", err)
		return
	}
	for _, projectCapacityUsage := range projectCapacityUsages {
		host, ok := hosts[projectCapacityUsage.ComputeHost]
		if !ok {
			slog.Warn("kvm_project_utilization: Compute host not found for project capacity usage", "compute_host", projectCapacityUsage.ComputeHost)
			continue
		}
		hostLabels := host.getHostLabels()
		hostLabels = append(hostLabels, projectCapacityUsage.ProjectID, projectCapacityUsage.ProjectName, projectCapacityUsage.DomainID, projectCapacityUsage.DomainName)

		ch <- prometheus.MustNewConstMetric(k.capacityUsagePerProjectAndHost, prometheus.GaugeValue, projectCapacityUsage.TotalVCPUs, append(hostLabels, "vcpu")...)
		ch <- prometheus.MustNewConstMetric(k.capacityUsagePerProjectAndHost, prometheus.GaugeValue, projectCapacityUsage.TotalRAMMB*1024*1024, append(hostLabels, "memory")...)
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

// queryProjectInstanceCount retrieves the number of running instances per project, hypervisor, and flavor on KVM from the database.
func (k *KVMProjectUtilizationKPI) queryProjectCapacityUsage() ([]kvmProjectCapacityUsage, error) {
	// This query will fetch all active instances. It will perform a join with the openstack projects to get the project name.
	// It will also join with the flavors table to get the flavor information, which is needed for the capacity usage metrics.
	// The results will be grouped by project, compute host, and availability zone to get the total capacity usage per project and hypervisor.
	// We will filter the results to only include instances that are running on KVM hypervisors by checking the compute host name pattern.
	// This assumes that all KVM hypervisors have a compute host name that follows the pattern "nodeXXX-bbYYY",
	// which is a naming convention in SAP Cloud Infrastructure and may need to be adjusted based on the actual environment.
	query := `
		SELECT
			s.tenant_id AS project_id,
			COALESCE(p.name, '') AS project_name,
			COALESCE(p.domain_id, '') AS domain_id,
			COALESCE(d.name, '') AS domain_name,
			s.os_ext_srv_attr_host AS compute_host,
			s.os_ext_az_availability_zone AS availability_zone,
			COALESCE(SUM(f.vcpus), 0) AS total_vcpus,
			COALESCE(SUM(f.ram), 0) AS total_ram_mb,
			COALESCE(SUM(f.disk), 0) AS total_disk_gb
		FROM ` + nova.Server{}.TableName() + ` s
		LEFT JOIN ` + nova.Flavor{}.TableName() + ` f ON s.flavor_name = f.name
		LEFT JOIN ` + identity.Project{}.TableName() + ` p ON p.id = s.tenant_id
		LEFT JOIN ` + identity.Domain{}.TableName() + ` d ON d.id = p.domain_id
		WHERE s.status NOT IN ('DELETED', 'ERROR')
		  AND s.os_ext_srv_attr_host LIKE '` + kvmComputeHostPattern + `'
		GROUP BY s.tenant_id, p.name, p.domain_id, d.name, s.os_ext_srv_attr_host, s.os_ext_az_availability_zone
	`
	var usages []kvmProjectCapacityUsage
	if _, err := k.DB.Select(&usages, query); err != nil {
		return nil, err
	}
	return usages, nil
}

// queryProjectInstanceCount retrieves the number of running instances per project, hypervisor, and flavor on KVM.
func (k *KVMProjectUtilizationKPI) queryProjectInstanceCount() ([]kvmProjectInstanceCount, error) {
	// This query will fetch all active instances. It will perform a join with the openstack projects to get the project name.
	// The results will be grouped by project, hypervisor, flavor, and availability zone to get the instance count.
	// We will filter the results to only include instances that are running on KVM hypervisors by checking the compute host name pattern.
	// This assumes that all KVM hypervisors have a compute host name that follows the pattern "nodeXXX-bbYYY",
	// which is a naming convention in SAP Cloud Infrastructure and may need to be adjusted based on the actual environment.
	query := `
		SELECT
			s.tenant_id AS project_id,
			COALESCE(p.name, '') AS project_name,
			COALESCE(p.domain_id, '') AS domain_id,
			COALESCE(d.name, '') AS domain_name,
			s.os_ext_srv_attr_host AS compute_host,
			s.os_ext_az_availability_zone AS availability_zone,
			s.flavor_name,
			COUNT(*) AS instance_count
		FROM ` + nova.Server{}.TableName() + ` s
		LEFT JOIN ` + identity.Project{}.TableName() + ` p ON p.id = s.tenant_id
		LEFT JOIN ` + identity.Domain{}.TableName() + ` d ON d.id = p.domain_id
		WHERE s.status NOT IN ('DELETED', 'ERROR')
		  AND s.os_ext_srv_attr_host LIKE '` + kvmComputeHostPattern + `'
		GROUP BY s.tenant_id, p.name, p.domain_id, d.name, s.os_ext_srv_attr_host, s.flavor_name, s.os_ext_az_availability_zone
	`
	var usages []kvmProjectInstanceCount
	if _, err := k.DB.Select(&usages, query); err != nil {
		return nil, err
	}
	return usages, nil
}
