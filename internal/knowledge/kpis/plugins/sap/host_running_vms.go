// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package sap

import (
	"context"
	"log/slog"
	"strconv"

	"github.com/cobaltcore-dev/cortex/api/v1alpha1"
	"github.com/cobaltcore-dev/cortex/internal/knowledge/extractor/plugins/sap"
	"github.com/cobaltcore-dev/cortex/internal/knowledge/extractor/plugins/shared"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/cobaltcore-dev/cortex/internal/knowledge/kpis/plugins"
	"github.com/cobaltcore-dev/cortex/pkg/conf"
	"github.com/cobaltcore-dev/cortex/pkg/db"
	"github.com/prometheus/client_golang/prometheus"
)

type HostRunningVMs struct {
	ComputeHostName  string  `db:"compute_host"`
	AvailabilityZone string  `db:"availability_zone"`
	CPUArchitecture  string  `db:"cpu_architecture"`
	HypervisorFamily string  `db:"hypervisor_family"`
	WorkloadType     string  `db:"workload_type"`
	Enabled          bool    `db:"enabled"`
	Decommissioned   bool    `db:"decommissioned"`
	ExternalCustomer bool    `db:"external_customer"`
	PinnedProjects   string  `db:"pinned_projects"`
	RunningVMs       float64 `db:"running_vms"`
	shared.HostUtilization
}

type HostRunningVMsKPI struct {
	// Common base for all KPIs that provides standard functionality.
	plugins.BaseKPI[struct{}] // No options passed through yaml config

	hostRunningVMsPerHost *prometheus.Desc
}

func (HostRunningVMsKPI) GetName() string {
	return "sap_host_running_vms_kpi"
}

func (k *HostRunningVMsKPI) Init(db *db.DB, client client.Client, opts conf.RawOpts) error {
	if err := k.BaseKPI.Init(db, client, opts); err != nil {
		return err
	}
	k.hostRunningVMsPerHost = prometheus.NewDesc(
		"cortex_sap_running_vms_per_host",
		"Current amount of running virtual machines on a host.",
		[]string{
			"compute_host",
			"availability_zone",
			"cpu_architecture",
			"workload_type",
			"hypervisor_family",
			"enabled",
			"decommissioned",
			"external_customer",
			"pinned_projects",
		},
		nil,
	)
	return nil
}

func (k *HostRunningVMsKPI) Describe(ch chan<- *prometheus.Desc) {
	ch <- k.hostRunningVMsPerHost
}

func (k *HostRunningVMsKPI) Collect(ch chan<- prometheus.Metric) {
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

	for _, utilization := range hostUtilizations {
		detail, exists := detailsByComputeHost[utilization.ComputeHost]
		if !exists {
			slog.Warn("host_running_vms: no host details for compute host", "compute_host", utilization.ComputeHost)
			continue
		}
		if utilization.TotalDiskAllocatableGB == 0 ||
			utilization.TotalRAMAllocatableMB == 0 ||
			utilization.TotalVCPUsAllocatable == 0 {
			slog.Info(
				"Skipping host since placement is reporting zero allocatable resources",
				"metric", "cortex_sap_running_vms_per_host",
				"host", utilization.ComputeHost,
				"cpu", utilization.TotalVCPUsAllocatable,
				"ram", utilization.TotalRAMAllocatableMB,
				"disk", utilization.TotalDiskAllocatableGB,
			)
			continue
		}
		if detail.HypervisorType == "ironic" {
			continue // Ignore ironic hosts
		}
		enabled := strconv.FormatBool(detail.Enabled)
		decommissioned := strconv.FormatBool(detail.Decommissioned)
		externalCustomer := strconv.FormatBool(detail.ExternalCustomer)
		pinnedProjects := ""
		if detail.PinnedProjects != nil {
			pinnedProjects = *detail.PinnedProjects
		}

		ch <- prometheus.MustNewConstMetric(
			k.hostRunningVMsPerHost,
			prometheus.GaugeValue,
			float64(detail.RunningVMs),
			utilization.ComputeHost,
			detail.AvailabilityZone,
			detail.CPUArchitecture,
			detail.WorkloadType,
			detail.HypervisorFamily,
			enabled,
			decommissioned,
			externalCustomer,
			pinnedProjects,
		)
	}
}
