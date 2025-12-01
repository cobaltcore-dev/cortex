// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package kvm

import (
	"log/slog"
	"strconv"

	"github.com/cobaltcore-dev/cortex/internal/knowledge/extractor/plugins/sap"
	"github.com/cobaltcore-dev/cortex/internal/knowledge/extractor/plugins/shared"

	"github.com/cobaltcore-dev/cortex/internal/knowledge/kpis/plugins"
	"github.com/cobaltcore-dev/cortex/pkg/conf"
	"github.com/cobaltcore-dev/cortex/pkg/db"
	"github.com/prometheus/client_golang/prometheus"
)

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

type HostCapacityUtilizedKPI struct {
	// Common base for all KPIs that provides standard functionality.
	plugins.BaseKPI[struct{}]   // No options passed through yaml config
	hostUtilizedCapacityPerHost *prometheus.Desc
}

func (HostCapacityUtilizedKPI) GetName() string {
	return "cortex_kvm_host_capacity_utilized_kpi"
}

func (k *HostCapacityUtilizedKPI) Init(db db.DB, opts conf.RawOpts) error {
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
	return nil
}

func (k *HostCapacityUtilizedKPI) Describe(ch chan<- *prometheus.Desc) {
	ch <- k.hostUtilizedCapacityPerHost
}

func (k *HostCapacityUtilizedKPI) Collect(ch chan<- prometheus.Metric) {
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
			COALESCE(hu.disk_used_gb, 0) AS disk_used_gb
		FROM ` + sap.HostDetails{}.TableName() + ` AS hd
		LEFT JOIN ` + shared.HostUtilization{}.TableName() + ` AS hu
		    ON hu.compute_host = hd.compute_host
		WHERE hd.hypervisor_type != 'ironic' AND hd.hypervisor_family = 'kvm';
    `
	if _, err := k.DB.Select(&hostUtilizedCapacity, query); err != nil {
		slog.Error("failed to select host total capacity", "err", err)
		return
	}

	for _, host := range hostUtilizedCapacity {
		if host.TotalRAMAllocatableMB == 0 || host.TotalVCPUsAllocatable == 0 || host.TotalDiskAllocatableGB == 0 {
			// Skip hosts with no capacity information
			continue
		}

		enabled := strconv.FormatBool(host.Enabled)
		decommissioned := strconv.FormatBool(host.Decommissioned)
		externalCustomer := strconv.FormatBool(host.ExternalCustomer)

		ch <- prometheus.MustNewConstMetric(
			k.hostUtilizedCapacityPerHost,
			prometheus.GaugeValue,
			host.VCPUsUsed,
			host.ComputeHostName,
			"cpu",
			host.AvailabilityZone,
			host.CPUArchitecture,
			host.WorkloadType,
			enabled,
			decommissioned,
			externalCustomer,
		)

		ch <- prometheus.MustNewConstMetric(
			k.hostUtilizedCapacityPerHost,
			prometheus.GaugeValue,
			host.RAMUsedMB,
			host.ComputeHostName,
			"ram",
			host.AvailabilityZone,
			host.CPUArchitecture,
			host.WorkloadType,
			enabled,
			decommissioned,
			externalCustomer,
		)

		ch <- prometheus.MustNewConstMetric(
			k.hostUtilizedCapacityPerHost,
			prometheus.GaugeValue,
			host.DiskUsedGB,
			host.ComputeHostName,
			"disk",
			host.AvailabilityZone,
			host.CPUArchitecture,
			host.WorkloadType,
			enabled,
			decommissioned,
			externalCustomer,
		)
	}
}
