// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package compute

import (
	"context"
	"log/slog"
	"strconv"
	"strings"

	"github.com/cobaltcore-dev/cortex/api/v1alpha1"
	"github.com/cobaltcore-dev/cortex/internal/knowledge/extractor/plugins/compute"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/cobaltcore-dev/cortex/internal/knowledge/kpis/plugins"
	"github.com/cobaltcore-dev/cortex/pkg/conf"
	"github.com/cobaltcore-dev/cortex/pkg/db"
	"github.com/prometheus/client_golang/prometheus"
)

func getBuildingBlock(hostName string) string {
	parts := strings.Split(hostName, "-")
	if len(parts) > 1 {
		return parts[1]
	}
	return "unknown"
}

type KVMResourceCapacityKPI struct {
	// Common base for all KPIs that provides standard functionality.
	plugins.BaseKPI[struct{}] // No options passed through yaml config
	utilizedCapacityPerHost   *prometheus.Desc
	paygCapacityPerHost       *prometheus.Desc
	failoverCapacityPerHost   *prometheus.Desc
	reservedCapacityPerHost   *prometheus.Desc
	totalCapacityPerHost      *prometheus.Desc
}

func (KVMResourceCapacityKPI) GetName() string {
	return "kvm_host_capacity_kpi"
}

func (k *KVMResourceCapacityKPI) Init(db *db.DB, client client.Client, opts conf.RawOpts) error {
	if err := k.BaseKPI.Init(db, client, opts); err != nil {
		return err
	}
	k.utilizedCapacityPerHost = prometheus.NewDesc(
		"cortex_kvm_host_capacity_utilized",
		"Utilized resources on the KVM hosts (individually by host).",
		[]string{
			"compute_host",
			"resource",
			"availability_zone",
			"building_block",
			"cpu_architecture",
			"workload_type",
			"enabled",
			"decommissioned",
			"external_customer",
		},
		nil,
	)
	k.paygCapacityPerHost = prometheus.NewDesc(
		"cortex_kvm_host_capacity_payg",
		"PAYG resources available on the KVM hosts (individually by host).",
		[]string{
			"compute_host",
			"resource",
			"availability_zone",
			"building_block",
			"cpu_architecture",
			"workload_type",
			"enabled",
			"decommissioned",
			"external_customer",
		},
		nil,
	)
	k.reservedCapacityPerHost = prometheus.NewDesc(
		"cortex_kvm_host_capacity_reserved",
		"Reserved resources on the KVM hosts (individually by host).",
		[]string{
			"compute_host",
			"resource",
			"availability_zone",
			"building_block",
			"cpu_architecture",
			"workload_type",
			"enabled",
			"decommissioned",
			"external_customer",
		},
		nil,
	)
	k.failoverCapacityPerHost = prometheus.NewDesc(
		"cortex_kvm_host_capacity_failover",
		"Failover resources on the KVM hosts (individually by host).",
		[]string{
			"compute_host",
			"resource",
			"availability_zone",
			"building_block",
			"cpu_architecture",
			"workload_type",
			"enabled",
			"decommissioned",
			"external_customer",
		},
		nil,
	)
	k.totalCapacityPerHost = prometheus.NewDesc(
		"cortex_kvm_host_capacity_total",
		"Total resources on the KVM hosts (individually by host).",
		[]string{
			"compute_host",
			"resource",
			"availability_zone",
			"building_block",
			"cpu_architecture",
			"workload_type",
			"enabled",
			"decommissioned",
			"external_customer",
		},
		nil,
	)
	return nil
}

func (k *KVMResourceCapacityKPI) Describe(ch chan<- *prometheus.Desc) {
	ch <- k.utilizedCapacityPerHost
	ch <- k.paygCapacityPerHost
	ch <- k.reservedCapacityPerHost
	ch <- k.failoverCapacityPerHost
	ch <- k.totalCapacityPerHost
}

func (k *KVMResourceCapacityKPI) Collect(ch chan<- prometheus.Metric) {
	// TODO use hypervisor CRD as data source
	hostDetailsKnowledge := &v1alpha1.Knowledge{}
	if err := k.Client.Get(
		context.Background(),
		client.ObjectKey{Name: "host-details"},
		hostDetailsKnowledge,
	); err != nil {
		slog.Error("failed to get knowledge host-details", "err", err)
		return
	}
	hostDetails, err := v1alpha1.
		UnboxFeatureList[compute.HostDetails](hostDetailsKnowledge.Status.Raw)
	if err != nil {
		slog.Error("failed to unbox storage pool cpu usage", "err", err)
		return
	}
	detailsByComputeHost := make(map[string]compute.HostDetails)
	for _, detail := range hostDetails {
		detailsByComputeHost[detail.ComputeHost] = detail
	}

	hostUtilizationKnowledge := &v1alpha1.Knowledge{}
	if err := k.Client.Get(
		context.Background(),
		client.ObjectKey{Name: "host-utilization"},
		hostUtilizationKnowledge,
	); err != nil {
		slog.Error("failed to get knowledge host-utilization", "err", err)
		return
	}
	hostUtilizations, err := v1alpha1.
		UnboxFeatureList[compute.HostUtilization](hostUtilizationKnowledge.Status.Raw)
	if err != nil {
		slog.Error("failed to unbox host utilization", "err", err)
		return
	}

	// TODO get all reservations and failover capacity crds
	for _, utilization := range hostUtilizations {
		host, exists := detailsByComputeHost[utilization.ComputeHost]
		if !exists {
			slog.Warn("no host details found for compute host", "compute_host", utilization.ComputeHost)
			continue
		}

		if host.HypervisorType == "ironic" || host.HypervisorFamily != "kvm" {
			continue
		}

		// TODO check if there is a flag for this in the hypervisor CRD
		if utilization.TotalRAMAllocatableMB == 0 || utilization.TotalVCPUsAllocatable == 0 || utilization.TotalDiskAllocatableGB == 0 {
			// Skip hosts with no capacity information
			slog.Warn("skipping host with zero total allocatable capacity", "compute_host", utilization.ComputeHost)
			continue
		}

		cpuUsed := utilization.VCPUsUsed
		ramUsed := utilization.RAMUsedMB
		diskUsed := utilization.DiskUsedGB

		exportCapacityMetricKVM(ch, k.utilizedCapacityPerHost, "cpu", cpuUsed, host)
		exportCapacityMetricKVM(ch, k.utilizedCapacityPerHost, "ram", ramUsed, host)
		exportCapacityMetricKVM(ch, k.utilizedCapacityPerHost, "disk", diskUsed, host)

		// WARNING: Using dummy data for now.
		// TODO Replace with actual data from reservations capacity CRDs
		cpuReserved := 100.0
		ramReserved := 1024.0
		diskReserved := 64.0

		exportCapacityMetricKVM(ch, k.reservedCapacityPerHost, "cpu", cpuReserved, host)
		exportCapacityMetricKVM(ch, k.reservedCapacityPerHost, "ram", ramReserved, host)
		exportCapacityMetricKVM(ch, k.reservedCapacityPerHost, "disk", diskReserved, host)

		// WARNING: Using dummy data for now.
		// TODO Replace with actual data from failover capacity CRDs
		cpuFailover := 100.0
		ramFailover := 1024.0
		diskFailover := 128.0

		exportCapacityMetricKVM(ch, k.failoverCapacityPerHost, "cpu", cpuFailover, host)
		exportCapacityMetricKVM(ch, k.failoverCapacityPerHost, "ram", ramFailover, host)
		exportCapacityMetricKVM(ch, k.failoverCapacityPerHost, "disk", diskFailover, host)

		totalCPU := utilization.TotalVCPUsAllocatable
		totalRAM := utilization.TotalRAMAllocatableMB
		totalDisk := utilization.TotalDiskAllocatableGB

		exportCapacityMetricKVM(ch, k.totalCapacityPerHost, "cpu", totalCPU, host)
		exportCapacityMetricKVM(ch, k.totalCapacityPerHost, "ram", totalRAM, host)
		exportCapacityMetricKVM(ch, k.totalCapacityPerHost, "disk", totalDisk, host)

		paygCPU := totalCPU - cpuUsed - cpuReserved - cpuFailover
		paygRAM := totalRAM - ramUsed - ramReserved - ramFailover
		paygDisk := totalDisk - diskUsed - diskReserved - diskFailover

		exportCapacityMetricKVM(ch, k.paygCapacityPerHost, "cpu", paygCPU, host)
		exportCapacityMetricKVM(ch, k.paygCapacityPerHost, "ram", paygRAM, host)
		exportCapacityMetricKVM(ch, k.paygCapacityPerHost, "disk", paygDisk, host)
	}
}

func exportCapacityMetricKVM(ch chan<- prometheus.Metric, metric *prometheus.Desc, resource string, value float64, host compute.HostDetails) {
	bb := getBuildingBlock(host.ComputeHost)

	enabled := strconv.FormatBool(host.Enabled)
	decommissioned := strconv.FormatBool(host.Decommissioned)
	externalCustomer := strconv.FormatBool(host.ExternalCustomer)

	ch <- prometheus.MustNewConstMetric(
		metric,
		prometheus.GaugeValue,
		value,
		host.ComputeHost,
		resource,
		host.AvailabilityZone,
		bb,
		host.CPUArchitecture,
		host.WorkloadType,
		enabled,
		decommissioned,
		externalCustomer,
	)
}
