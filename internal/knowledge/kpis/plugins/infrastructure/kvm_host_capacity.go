// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package infrastructure

import (
	"context"

	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/cobaltcore-dev/cortex/api/v1alpha1"
	"github.com/cobaltcore-dev/cortex/internal/knowledge/db"
	"github.com/cobaltcore-dev/cortex/internal/knowledge/kpis/plugins"
	"github.com/cobaltcore-dev/cortex/pkg/capacity"
	"github.com/cobaltcore-dev/cortex/pkg/conf"
	"github.com/prometheus/client_golang/prometheus"

	hv1 "github.com/cobaltcore-dev/openstack-hypervisor-operator/api/v1"
)

type KVMHostCapacityKPI struct {
	// Common base for all KPIs that provides standard functionality.
	plugins.BaseKPI[struct{}] // No options passed through yaml config
	totalCapacityPerHost      *prometheus.Desc
	capacityPerHost           *prometheus.Desc
}

func (KVMHostCapacityKPI) GetName() string {
	return "kvm_host_capacity_kpi"
}

func (k *KVMHostCapacityKPI) Init(db *db.DB, client client.Client, opts conf.RawOpts) error {
	if err := k.BaseKPI.Init(db, client, opts); err != nil {
		return err
	}
	k.totalCapacityPerHost = prometheus.NewDesc(
		"cortex_kvm_host_capacity_total",
		"Total resource capacity on the KVM hosts (individually by host). CPU in vCPUs, memory in bytes.",
		append(kvmHostLabels, "resource"),
		nil,
	)
	k.capacityPerHost = prometheus.NewDesc(
		"cortex_kvm_host_capacity_usage",
		"Resource capacity usage on the KVM hosts (individually by host). CPU in vCPUs, memory in bytes.",
		append(kvmHostLabels, "resource", "type"),
		nil,
	)
	return nil
}

func (k *KVMHostCapacityKPI) Describe(ch chan<- *prometheus.Desc) {
	ch <- k.totalCapacityPerHost
	ch <- k.capacityPerHost
}

func (k *KVMHostCapacityKPI) Collect(ch chan<- prometheus.Metric) {
	rawHypervisors := &hv1.HypervisorList{}
	if err := k.Client.List(context.Background(), rawHypervisors); err != nil {
		log.Error(err, "failed to list hypervisors")
		return
	}

	hypervisors := make([]kvmHost, len(rawHypervisors.Items))
	for i, hv := range rawHypervisors.Items {
		hypervisors[i] = kvmHost{Hypervisor: hv}
	}

	unfilteredReservations := &v1alpha1.ReservationList{}
	if err := k.Client.List(context.Background(), unfilteredReservations); err != nil {
		log.Error(err, "failed to list reservations")
		return
	}

	// Only Ready Nova reservations count toward the KPI's reserved/failover views, matching
	// the capacity the scheduler actually blocks.
	reservations := make([]v1alpha1.Reservation, 0, len(unfilteredReservations.Items))
	for _, reservation := range unfilteredReservations.Items {
		if reservation.Spec.SchedulingDomain == v1alpha1.SchedulingDomainNova && reservation.IsReady() {
			reservations = append(reservations, reservation)
		}
	}

	for _, hypervisor := range hypervisors {
		rep := capacity.NewHostUsageReport(hypervisor.Hypervisor, reservations, false)
		if _, hasCPU := rep.Total[hv1.ResourceCPU]; !hasCPU {
			log.V(1).Info("hypervisor missing cpu or ram capacity, skipping", "host", hypervisor.Name)
			continue
		}
		if _, hasRAM := rep.Total[hv1.ResourceMemory]; !hasRAM {
			log.V(1).Info("hypervisor missing cpu or ram capacity, skipping", "host", hypervisor.Name)
			continue
		}
		labels := hypervisor.getHostLabels()

		cpuTotal := rep.Total[hv1.ResourceCPU]
		ramTotal := rep.Total[hv1.ResourceMemory]
		ch <- prometheus.MustNewConstMetric(k.totalCapacityPerHost, prometheus.GaugeValue, cpuTotal.AsApproximateFloat64(), append(labels, "cpu")...)
		ch <- prometheus.MustNewConstMetric(k.totalCapacityPerHost, prometheus.GaugeValue, ramTotal.AsApproximateFloat64(), append(labels, "ram")...)

		cpuUsed := rep.Used[hv1.ResourceCPU]
		ramUsed := rep.Used[hv1.ResourceMemory]
		ch <- prometheus.MustNewConstMetric(k.capacityPerHost, prometheus.GaugeValue, cpuUsed.AsApproximateFloat64(), append(labels, "cpu", "utilized")...)
		ch <- prometheus.MustNewConstMetric(k.capacityPerHost, prometheus.GaugeValue, ramUsed.AsApproximateFloat64(), append(labels, "ram", "utilized")...)

		cpuReserved := rep.Reserved[hv1.ResourceCPU]
		ramReserved := rep.Reserved[hv1.ResourceMemory]
		ch <- prometheus.MustNewConstMetric(k.capacityPerHost, prometheus.GaugeValue, cpuReserved.AsApproximateFloat64(), append(labels, "cpu", "reserved")...)
		ch <- prometheus.MustNewConstMetric(k.capacityPerHost, prometheus.GaugeValue, ramReserved.AsApproximateFloat64(), append(labels, "ram", "reserved")...)

		cpuFailover := rep.Failover[hv1.ResourceCPU]
		ramFailover := rep.Failover[hv1.ResourceMemory]
		ch <- prometheus.MustNewConstMetric(k.capacityPerHost, prometheus.GaugeValue, cpuFailover.AsApproximateFloat64(), append(labels, "cpu", "failover")...)
		ch <- prometheus.MustNewConstMetric(k.capacityPerHost, prometheus.GaugeValue, ramFailover.AsApproximateFloat64(), append(labels, "ram", "failover")...)

		cpuAvailable := rep.Available[hv1.ResourceCPU]
		ramAvailable := rep.Available[hv1.ResourceMemory]
		ch <- prometheus.MustNewConstMetric(k.capacityPerHost, prometheus.GaugeValue, cpuAvailable.AsApproximateFloat64(), append(labels, "cpu", "available")...)
		ch <- prometheus.MustNewConstMetric(k.capacityPerHost, prometheus.GaugeValue, ramAvailable.AsApproximateFloat64(), append(labels, "ram", "available")...)
	}
}

func init() {
	plugins.Index["kvm_host_capacity_kpi"] = func() plugins.KPI { return &KVMHostCapacityKPI{} }
}
