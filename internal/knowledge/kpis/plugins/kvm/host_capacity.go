// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package kvm

import (
	"context"
	"log/slog"
	"strconv"
	"strings"

	"github.com/cobaltcore-dev/cortex/api/v1alpha1"
	"github.com/cobaltcore-dev/cortex/internal/knowledge/extractor/plugins/sap"
	"github.com/cobaltcore-dev/cortex/internal/knowledge/extractor/plugins/shared"
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

type HostCapacityKPI struct {
	// Common base for all KPIs that provides standard functionality.
	plugins.BaseKPI[struct{}]   // No options passed through yaml config
	hostUtilizedCapacityPerHost *prometheus.Desc
	hostPAYGCapacityPerHost     *prometheus.Desc
	hostFailoverCapacityPerHost *prometheus.Desc
	hostReservedCapacityPerHost *prometheus.Desc
	hostTotalCapacityPerHost    *prometheus.Desc
}

func (HostCapacityKPI) GetName() string {
	return "cortex_kvm_host_capacity_kpi"
}

func (k *HostCapacityKPI) Init(db *db.DB, client client.Client, opts conf.RawOpts) error {
	if err := k.BaseKPI.Init(db, client, opts); err != nil {
		return err
	}
	k.hostUtilizedCapacityPerHost = prometheus.NewDesc(
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
	k.hostPAYGCapacityPerHost = prometheus.NewDesc(
		"cortex_kvm_host_capacity_payg_allocatable",
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
	k.hostReservedCapacityPerHost = prometheus.NewDesc(
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
	k.hostFailoverCapacityPerHost = prometheus.NewDesc(
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
	k.hostTotalCapacityPerHost = prometheus.NewDesc(
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

func (k *HostCapacityKPI) Describe(ch chan<- *prometheus.Desc) {
	ch <- k.hostUtilizedCapacityPerHost
	ch <- k.hostPAYGCapacityPerHost
	ch <- k.hostReservedCapacityPerHost
	ch <- k.hostFailoverCapacityPerHost
	ch <- k.hostTotalCapacityPerHost
}

func (k *HostCapacityKPI) Collect(ch chan<- prometheus.Metric) {
	// TODO use hypervisor CRD as data source
	hostDetailsKnowledge := &v1alpha1.Knowledge{}
	if err := k.Client.Get(
		context.Background(),
		client.ObjectKey{Name: "sap-host-details"},
		hostDetailsKnowledge,
	); err != nil {
		slog.Error("failed to get knowledge sap-host-details", "err", err)
		return
	}
	hostDetails, err := v1alpha1.
		UnboxFeatureList[sap.HostDetails](hostDetailsKnowledge.Status.Raw)
	if err != nil {
		slog.Error("failed to unbox storage pool cpu usage", "err", err)
		return
	}
	detailsByComputeHost := make(map[string]sap.HostDetails)
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
		UnboxFeatureList[shared.HostUtilization](hostUtilizationKnowledge.Status.Raw)
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

		export(ch, k.hostUtilizedCapacityPerHost, "cpu", cpuUsed, host)
		export(ch, k.hostUtilizedCapacityPerHost, "ram", ramUsed, host)
		export(ch, k.hostUtilizedCapacityPerHost, "disk", diskUsed, host)

		// WARNING: Using dummy data for now.
		// TODO Replace with actual data from reservations capacity CRDs
		cpuReserved := 100.0
		ramReserved := 1024.0
		diskReserved := 64.0

		export(ch, k.hostReservedCapacityPerHost, "cpu", cpuReserved, host)
		export(ch, k.hostReservedCapacityPerHost, "ram", ramReserved, host)
		export(ch, k.hostReservedCapacityPerHost, "disk", diskReserved, host)

		// WARNING: Using dummy data for now.
		// TODO Replace with actual data from failover capacity CRDs
		cpuFailover := 100.0
		ramFailover := 1024.0
		diskFailover := 128.0

		export(ch, k.hostFailoverCapacityPerHost, "cpu", cpuFailover, host)
		export(ch, k.hostFailoverCapacityPerHost, "ram", ramFailover, host)
		export(ch, k.hostFailoverCapacityPerHost, "disk", diskFailover, host)

		totalCPU := utilization.TotalVCPUsAllocatable
		totalRAM := utilization.TotalRAMAllocatableMB
		totalDisk := utilization.TotalDiskAllocatableGB

		export(ch, k.hostTotalCapacityPerHost, "cpu", totalCPU, host)
		export(ch, k.hostTotalCapacityPerHost, "ram", totalRAM, host)
		export(ch, k.hostTotalCapacityPerHost, "disk", totalDisk, host)

		paygCPU := totalCPU - cpuUsed - cpuReserved - cpuFailover
		paygRAM := totalRAM - ramUsed - ramReserved - ramFailover
		paygDisk := totalDisk - diskUsed - diskReserved - diskFailover

		export(ch, k.hostPAYGCapacityPerHost, "cpu", paygCPU, host)
		export(ch, k.hostPAYGCapacityPerHost, "ram", paygRAM, host)
		export(ch, k.hostPAYGCapacityPerHost, "disk", paygDisk, host)
	}
}

func export(ch chan<- prometheus.Metric, metric *prometheus.Desc, resource string, value float64, host sap.HostDetails) {
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
