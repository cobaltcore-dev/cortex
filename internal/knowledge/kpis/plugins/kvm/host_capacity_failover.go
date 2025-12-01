// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package kvm

import (
	"log/slog"
	"strconv"

	"github.com/cobaltcore-dev/cortex/internal/knowledge/extractor/plugins/sap"

	"github.com/cobaltcore-dev/cortex/internal/knowledge/kpis/plugins"
	"github.com/cobaltcore-dev/cortex/pkg/conf"
	"github.com/cobaltcore-dev/cortex/pkg/db"
	"github.com/prometheus/client_golang/prometheus"
)

type hostFailoverCapacity struct {
	ComputeHostName  string `db:"compute_host"`
	AvailabilityZone string `db:"availability_zone"`
	CPUArchitecture  string `db:"cpu_architecture"`
	WorkloadType     string `db:"workload_type"`
	BuildingBlock    string `db:"building_block"`
	Enabled          bool   `db:"enabled"`
	Decommissioned   bool   `db:"decommissioned"`
	ExternalCustomer bool   `db:"external_customer"`
}

type HostCapacityFailoverKPI struct {
	// Common base for all KPIs that provides standard functionality.
	plugins.BaseKPI[struct{}]   // No options passed through yaml config
	hostFailoverCapacityPerHost *prometheus.Desc
}

func (HostCapacityFailoverKPI) GetName() string {
	return "cortex_kvm_host_capacity_failover_kpi"
}

func (k *HostCapacityFailoverKPI) Init(db db.DB, opts conf.RawOpts) error {
	if err := k.BaseKPI.Init(db, opts); err != nil {
		return err
	}
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

func (k *HostCapacityFailoverKPI) Describe(ch chan<- *prometheus.Desc) {
	ch <- k.hostFailoverCapacityPerHost
}

func (k *HostCapacityFailoverKPI) Collect(ch chan<- prometheus.Metric) {
	// TODO use fail over CRD (?) as data source
	var hostFailoverCapacity []hostFailoverCapacity

	query := `
		SELECT
    		hd.compute_host,
    		hd.availability_zone,
    		hd.cpu_architecture,
    		hd.workload_type,
    		hd.enabled,
			hd.decommissioned,
			hd.external_customer
		FROM ` + sap.HostDetails{}.TableName() + `
		WHERE hd.hypervisor_type != 'ironic' AND hd.hypervisor_family = 'kvm';
    `
	if _, err := k.DB.Select(&hostFailoverCapacity, query); err != nil {
		slog.Error("failed to select host total capacity", "err", err)
		return
	}

	for _, host := range hostFailoverCapacity {
		enabled := strconv.FormatBool(host.Enabled)
		decommissioned := strconv.FormatBool(host.Decommissioned)
		externalCustomer := strconv.FormatBool(host.ExternalCustomer)

		ch <- prometheus.MustNewConstMetric(
			k.hostFailoverCapacityPerHost,
			prometheus.GaugeValue,
			10, // TODO this is a placeholder value
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
			k.hostFailoverCapacityPerHost,
			prometheus.GaugeValue,
			2048, // TODO this is a placeholder value
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
			k.hostFailoverCapacityPerHost,
			prometheus.GaugeValue,
			100, // TODO this is a placeholder value
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
