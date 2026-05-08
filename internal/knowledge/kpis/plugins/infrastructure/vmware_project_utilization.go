// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package infrastructure

import (
	"context"
	"log/slog"

	"github.com/cobaltcore-dev/cortex/api/v1alpha1"
	"github.com/cobaltcore-dev/cortex/internal/knowledge/datasources/plugins/openstack/identity"
	"github.com/cobaltcore-dev/cortex/internal/knowledge/datasources/plugins/openstack/nova"
	"github.com/cobaltcore-dev/cortex/internal/knowledge/db"
	"github.com/cobaltcore-dev/cortex/internal/knowledge/extractor/plugins/compute"
	"github.com/cobaltcore-dev/cortex/internal/knowledge/kpis/plugins"
	"github.com/cobaltcore-dev/cortex/pkg/conf"
	"github.com/prometheus/client_golang/prometheus"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

type vmwareProjectInstanceCount struct {
	ProjectID        string  `db:"project_id"`
	ProjectName      string  `db:"project_name"`
	DomainID         string  `db:"domain_id"`
	DomainName       string  `db:"domain_name"`
	ComputeHost      string  `db:"compute_host"`
	FlavorName       string  `db:"flavor_name"`
	AvailabilityZone string  `db:"availability_zone"`
	InstanceCount    float64 `db:"instance_count"`
}

type vmwareProjectCapacityUsage struct {
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

func (k *VMwareProjectUtilizationKPI) Init(dbConn *db.DB, c client.Client, opts conf.RawOpts) error {
	if err := k.BaseKPI.Init(dbConn, c, opts); err != nil {
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
	hosts, err := k.getVMwareHosts()
	if err != nil {
		// Log the error and return early to avoid panicking. The KPI will be retried on the next scrape.
		slog.Error("vmware_project_utilization: Failed to get VMware hosts for project utilization KPI", "error", err)
		return
	}

	// Export project x flavor x compute_host instance count metric
	projectInstanceCounts, err := k.queryProjectInstanceCount()
	if err != nil {
		slog.Error("vmware_project_utilization: Failed to query project instance count for project utilization KPI", "error", err)
		return
	}
	for _, projectInstanceCount := range projectInstanceCounts {
		host, ok := hosts[projectInstanceCount.ComputeHost]
		if !ok {
			slog.Warn("vmware_project_utilization: Compute host not found for project instance count", "compute_host", projectInstanceCount.ComputeHost)
			continue
		}
		hostLabels := host.getHostLabels()
		hostLabels = append(hostLabels, projectInstanceCount.ProjectID, projectInstanceCount.ProjectName, projectInstanceCount.DomainID, projectInstanceCount.DomainName, projectInstanceCount.FlavorName)
		ch <- prometheus.MustNewConstMetric(k.instanceCountPerProjectAndHostAndFlavor, prometheus.GaugeValue, projectInstanceCount.InstanceCount, hostLabels...)
	}

	// Export project x compute_host x resource capacity usage metric
	projectCapacityUsages, err := k.queryProjectCapacityUsage()
	if err != nil {
		slog.Error("vmware_project_utilization: Failed to query project capacity usage for project utilization KPI", "error", err)
		return
	}
	for _, projectCapacityUsage := range projectCapacityUsages {
		host, ok := hosts[projectCapacityUsage.ComputeHost]
		if !ok {
			slog.Warn("vmware_project_utilization: Compute host not found for project capacity usage", "compute_host", projectCapacityUsage.ComputeHost)
			continue
		}
		hostLabels := host.getHostLabels()
		hostLabels = append(hostLabels, projectCapacityUsage.ProjectID, projectCapacityUsage.ProjectName, projectCapacityUsage.DomainID, projectCapacityUsage.DomainName)

		ch <- prometheus.MustNewConstMetric(k.capacityUsagePerProjectAndHost, prometheus.GaugeValue, projectCapacityUsage.TotalVCPUs, append(hostLabels, "cpu")...)
		ch <- prometheus.MustNewConstMetric(k.capacityUsagePerProjectAndHost, prometheus.GaugeValue, projectCapacityUsage.TotalRAMMB*1024*1024, append(hostLabels, "ram")...)
		ch <- prometheus.MustNewConstMetric(k.capacityUsagePerProjectAndHost, prometheus.GaugeValue, projectCapacityUsage.TotalDiskGB*1024*1024*1024, append(hostLabels, "disk")...)
	}
}

// getVMwareHosts retrieves the mapping of VMware hypervisors to their corresponding host information
func (k *VMwareProjectUtilizationKPI) getVMwareHosts() (map[string]vmwareHost, error) {
	knowledge := &v1alpha1.Knowledge{}
	if err := k.Client.Get(context.Background(), client.ObjectKey{Name: hostDetailsKnowledgeName}, knowledge); err != nil {
		return nil, err
	}

	hostDetails, err := v1alpha1.UnboxFeatureList[compute.HostDetails](knowledge.Status.Raw)
	if err != nil {
		return nil, err
	}

	hostMapping := make(map[string]vmwareHost)
	for _, host := range hostDetails {
		if host.HypervisorType == vmwareIronicHypervisorType || host.HypervisorFamily != hypervisorFamilyVMware {
			continue
		}
		hostMapping[host.ComputeHost] = vmwareHost{HostDetails: host}
	}

	return hostMapping, nil
}

// queryProjectInstanceCount retrieves the number of running instances per project, hypervisor, and flavor on VMware from the database.
func (k *VMwareProjectUtilizationKPI) queryProjectCapacityUsage() ([]vmwareProjectCapacityUsage, error) {
	// This query will fetch all active instances. It will perform a join with the openstack projects to get the project name.
	// It will also join with the flavors table to get the flavor information, which is needed for the capacity usage metrics.
	// The results will be grouped by project, compute host, and availability zone to get the total capacity usage per project and hypervisor.
	// We will filter the results to only include instances that are running on VMware hypervisors by checking the compute host name pattern.
	// This assumes that all VMware hypervisors have a compute host name that starts with "nova-compute-",
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
		  AND s.os_ext_srv_attr_host LIKE '` + vmwareComputeHostPattern + `'
		  AND s.os_ext_srv_attr_host NOT LIKE '` + vmwareIronicComputeHostPattern + `'
		GROUP BY s.tenant_id, p.name, p.domain_id, d.name, s.os_ext_srv_attr_host, s.os_ext_az_availability_zone
	`
	var usages []vmwareProjectCapacityUsage
	if _, err := k.DB.Select(&usages, query); err != nil {
		return nil, err
	}
	return usages, nil
}

// queryProjectInstanceCount retrieves the number of running instances per project, hypervisor, and flavor on VMware.
func (k *VMwareProjectUtilizationKPI) queryProjectInstanceCount() ([]vmwareProjectInstanceCount, error) {
	// This query will fetch all active instances. It will perform a join with the openstack projects to get the project name.
	// The results will be grouped by project, hypervisor, flavor, and availability zone to get the instance count.
	// We will filter the results to only include instances that are running on VMware hypervisors by checking the compute host name pattern.
	// This assumes that all VMware hypervisors have a compute host name that starts with "nova-compute-",
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
		  AND s.os_ext_srv_attr_host LIKE '` + vmwareComputeHostPattern + `'
		  AND s.os_ext_srv_attr_host NOT LIKE '` + vmwareIronicComputeHostPattern + `'
		GROUP BY s.tenant_id, p.name, p.domain_id, d.name, s.os_ext_srv_attr_host, s.flavor_name, s.os_ext_az_availability_zone
	`
	var usages []vmwareProjectInstanceCount
	if _, err := k.DB.Select(&usages, query); err != nil {
		return nil, err
	}
	return usages, nil
}
