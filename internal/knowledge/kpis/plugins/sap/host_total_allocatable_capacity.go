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

type HostTotalAllocatableCapacityKPI struct {
	// Common base for all KPIs that provides standard functionality.
	plugins.BaseKPI[struct{}] // No options passed through yaml config

	hostTotalCapacityPerHost *prometheus.Desc
}

func (HostTotalAllocatableCapacityKPI) GetName() string {
	return "sap_host_total_allocatable_capacity_kpi"
}

func (k *HostTotalAllocatableCapacityKPI) Init(db *db.DB, client client.Client, opts conf.RawOpts) error {
	if err := k.BaseKPI.Init(db, client, opts); err != nil {
		return err
	}
	k.hostTotalCapacityPerHost = prometheus.NewDesc(
		"cortex_sap_total_allocatable_capacity_per_host",
		"Total resources available on the hosts currently (individually by host).",
		[]string{
			"compute_host",
			"resource",
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

func (k *HostTotalAllocatableCapacityKPI) Describe(ch chan<- *prometheus.Desc) {
	ch <- k.hostTotalCapacityPerHost
}

func (k *HostTotalAllocatableCapacityKPI) Collect(ch chan<- prometheus.Metric) {
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
			slog.Warn("host_total_allocatable_capacity: no host details for compute host", "compute_host", utilization.ComputeHost)
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
			k.hostTotalCapacityPerHost,
			prometheus.GaugeValue,
			float64(utilization.TotalRAMAllocatableMB),
			utilization.ComputeHost,
			"ram",
			detail.AvailabilityZone,
			detail.CPUArchitecture,
			detail.WorkloadType,
			detail.HypervisorFamily,
			enabled,
			decommissioned,
			externalCustomer,
			pinnedProjects,
		)
		ch <- prometheus.MustNewConstMetric(
			k.hostTotalCapacityPerHost,
			prometheus.GaugeValue,
			float64(utilization.TotalVCPUsAllocatable),
			utilization.ComputeHost,
			"cpu",
			detail.AvailabilityZone,
			detail.CPUArchitecture,
			detail.WorkloadType,
			detail.HypervisorFamily,
			enabled,
			decommissioned,
			externalCustomer,
			pinnedProjects,
		)
		ch <- prometheus.MustNewConstMetric(
			k.hostTotalCapacityPerHost,
			prometheus.GaugeValue,
			float64(utilization.TotalDiskAllocatableGB),
			utilization.ComputeHost,
			"disk",
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
