// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package sap

import (
	"log/slog"
	"strconv"

	"github.com/cobaltcore-dev/cortex/internal/extractor/plugins/sap"

	"github.com/cobaltcore-dev/cortex/internal/conf"
	"github.com/cobaltcore-dev/cortex/internal/db"
	"github.com/cobaltcore-dev/cortex/internal/kpis/plugins"
	"github.com/prometheus/client_golang/prometheus"
)

type HostRunningVMs struct {
	ComputeHostName  string  `db:"compute_host"`
	AvailabilityZone string  `db:"availability_zone"`
	CPUArchitecture  string  `db:"cpu_architecture"`
	HypervisorFamily string  `db:"hypervisor_family"`
	WorkloadType     string  `db:"workload_type"`
	Enabled          bool    `db:"enabled"`
	RunningVMs       float64 `db:"running_vms"`
}

type HostRunningVMsKPI struct {
	// Common base for all KPIs that provides standard functionality.
	plugins.BaseKPI[struct{}] // No options passed through yaml config

	hostRunningVMsPerHost *prometheus.Desc
}

func (HostRunningVMsKPI) GetName() string {
	return "sap_host_running_vms_kpi"
}

func (k *HostRunningVMsKPI) Init(db db.DB, opts conf.RawOpts) error {
	if err := k.BaseKPI.Init(db, opts); err != nil {
		return err
	}
	k.hostRunningVMsPerHost = prometheus.NewDesc(
		"cortex_sap_host_running_vms_per_host_pct",
		"Resources utilized on the hosts currently (individually by host).",
		[]string{
			"compute_host",
			"availability_zone",
			"cpu_architecture",
			"workload_type",
			"hypervisor_family",
			"enabled",
		},
		nil,
	)
	return nil
}

func (k *HostRunningVMsKPI) Describe(ch chan<- *prometheus.Desc) {
	ch <- k.hostRunningVMsPerHost
}

func (k *HostRunningVMsKPI) Collect(ch chan<- prometheus.Metric) {
	var hostRunningVMs []HostRunningVMs

	query := `
		SELECT
    		compute_host,
    		availability_zone,
    		cpu_architecture,
    		hypervisor_family,
    		workload_type,
    		enabled,
    		running_vms
		FROM ` + sap.HostDetails{}.TableName() + `
		WHERE hypervisor_type != 'ironic';
    `
	if _, err := k.DB.Select(&hostRunningVMs, query); err != nil {
		slog.Error("failed to select host utilization", "err", err)
		return
	}

	for _, host := range hostRunningVMs {

		enabled := strconv.FormatBool(host.Enabled)

		ch <- prometheus.MustNewConstMetric(
			k.hostRunningVMsPerHost,
			prometheus.GaugeValue,
			host.RunningVMs,
			host.ComputeHostName,
			host.AvailabilityZone,
			host.CPUArchitecture,
			host.WorkloadType,
			host.HypervisorFamily,
			enabled,
		)
	}
}
