// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package kvm

import (
	"log/slog"
	"strconv"
	"strings"

	"github.com/cobaltcore-dev/cortex/internal/knowledge/extractor/plugins/sap"
	"github.com/cobaltcore-dev/cortex/internal/knowledge/extractor/plugins/shared"

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

type hostUtilizedCapacity struct {
	ComputeHostName  string `db:"compute_host"`
	AvailabilityZone string `db:"availability_zone"`
	CPUArchitecture  string `db:"cpu_architecture"`
	WorkloadType     string `db:"workload_type"`
	BuildingBlock    string `db:"building_block"`
	Enabled          bool   `db:"enabled"`
	Decommissioned   bool   `db:"decommissioned"`
	ExternalCustomer bool   `db:"external_customer"`
	shared.HostUtilization
}

type HostCapacityKPI struct {
	// Common base for all KPIs that provides standard functionality.
	plugins.BaseKPI[struct{}]   // No options passed through yaml config
	hostUtilizedCapacityPerHost *prometheus.Desc
	hostTotalCapacityPerHost    *prometheus.Desc
	hostFailoverCapacityPerHost *prometheus.Desc
	hostReservedCapacityPerHost *prometheus.Desc
}

func (HostCapacityKPI) GetName() string {
	return "cortex_kvm_host_capacity_kpi"
}

func (k *HostCapacityKPI) Init(db db.DB, opts conf.RawOpts) error {
	if err := k.BaseKPI.Init(db, opts); err != nil {
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
	k.hostTotalCapacityPerHost = prometheus.NewDesc(
		"cortex_kvm_host_capacity_total_allocatable",
		"Total resources available on the KVM hosts (individually by host).",
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
	return nil
}

func (k *HostCapacityKPI) Describe(ch chan<- *prometheus.Desc) {
	ch <- k.hostUtilizedCapacityPerHost
}

func (k *HostCapacityKPI) Collect(ch chan<- prometheus.Metric) {
	// TODO use hypervisor CRD as data source
	var hostUtilizedCapacity []hostUtilizedCapacity

	query := `
		SELECT
    		hd.compute_host,
    		hd.availability_zone,
    		hd.cpu_architecture,
    		hd.workload_type,
    		hd.enabled,
			hd.decommissioned,
			hd.external_customer,
            COALESCE(hu.ram_used_mb, 0) AS ram_used_mb,
			COALESCE(hu.vcpus_used, 0) AS vcpus_used,
			COALESCE(hu.disk_used_gb, 0) AS disk_used_gb,
            COALESCE(hu.total_ram_allocatable_mb, 0) AS total_ram_allocatable_mb,
			COALESCE(hu.total_vcpus_allocatable, 0) AS total_vcpus_allocatable,
			COALESCE(hu.total_disk_allocatable_gb, 0) AS total_disk_allocatable_gb
		FROM ` + sap.HostDetails{}.TableName() + ` AS hd
		LEFT JOIN ` + shared.HostUtilization{}.TableName() + ` AS hu
		    ON hu.compute_host = hd.compute_host
		WHERE hd.hypervisor_type != 'ironic' AND hd.hypervisor_family = 'kvm';
    `
	if _, err := k.DB.Select(&hostUtilizedCapacity, query); err != nil {
		slog.Error("failed to select host total capacity", "err", err)
		return
	}

	// TODO get all reservations and failover capacity crds

	for _, host := range hostUtilizedCapacity {
		// TODO check if there is a flag for this in the hypervisor CRD
		if host.TotalRAMAllocatableMB == 0 || host.TotalVCPUsAllocatable == 0 || host.TotalDiskAllocatableGB == 0 {
			// Skip hosts with no capacity information
			continue
		}

		export(ch, k.hostUtilizedCapacityPerHost, "cpu", host.VCPUsUsed, host)
		export(ch, k.hostUtilizedCapacityPerHost, "ram", host.RAMUsedMB, host)
		export(ch, k.hostUtilizedCapacityPerHost, "disk", host.DiskUsedGB, host)

		export(ch, k.hostTotalCapacityPerHost, "cpu", host.TotalVCPUsAllocatable, host)
		export(ch, k.hostTotalCapacityPerHost, "ram", host.TotalRAMAllocatableMB, host)
		export(ch, k.hostTotalCapacityPerHost, "disk", host.TotalDiskAllocatableGB, host)

		// TODO join reservations and failover capacity crds with the current hypervisor
		// TODO USING DUMMY VALUES FOR NOW
		export(ch, k.hostReservedCapacityPerHost, "cpu", 100, host)
		export(ch, k.hostReservedCapacityPerHost, "ram", 1024, host)
		export(ch, k.hostReservedCapacityPerHost, "disk", 64, host)

		export(ch, k.hostFailoverCapacityPerHost, "cpu", 100, host)
		export(ch, k.hostFailoverCapacityPerHost, "ram", 2048, host)
		export(ch, k.hostFailoverCapacityPerHost, "disk", 128, host)
	}
}

func export(ch chan<- prometheus.Metric, metric *prometheus.Desc, resource string, value float64, host hostUtilizedCapacity) {
	bb := getBuildingBlock(host.ComputeHostName)

	enabled := strconv.FormatBool(host.Enabled)
	decommissioned := strconv.FormatBool(host.Decommissioned)
	externalCustomer := strconv.FormatBool(host.ExternalCustomer)

	ch <- prometheus.MustNewConstMetric(
		metric,
		prometheus.GaugeValue,
		value,
		host.ComputeHostName,
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
