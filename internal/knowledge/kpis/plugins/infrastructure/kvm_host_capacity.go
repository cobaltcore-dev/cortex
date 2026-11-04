// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package infrastructure

import (
	"context"

	"k8s.io/apimachinery/pkg/api/resource"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/cobaltcore-dev/cortex/api/v1alpha1"
	"github.com/cobaltcore-dev/cortex/internal/knowledge/db"
	"github.com/cobaltcore-dev/cortex/internal/knowledge/kpis/plugins"
	"github.com/cobaltcore-dev/cortex/internal/scheduling/capacity"
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

// novaReservations returns the subset of reservations in the Nova scheduling domain.
// The shared capacity math is domain-agnostic; this KPI only accounts for Nova.
func novaReservations(reservations []v1alpha1.Reservation) []v1alpha1.Reservation {
	out := make([]v1alpha1.Reservation, 0, len(reservations))
	for _, r := range reservations {
		if r.Spec.SchedulingDomain == v1alpha1.SchedulingDomainNova {
			out = append(out, r)
		}
	}
	return out
}

func (k *KVMHostCapacityKPI) getHypervisors() ([]kvmHost, error) {
	hvs := &hv1.HypervisorList{}
	if err := k.Client.List(context.Background(), hvs); err != nil {
		return nil, err
	}

	hosts := make([]kvmHost, len(hvs.Items))
	for i, hv := range hvs.Items {
		hosts[i] = kvmHost{Hypervisor: hv}
	}
	return hosts, nil
}

func (k *KVMHostCapacityKPI) Collect(ch chan<- prometheus.Metric) {
	hypervisors, err := k.getHypervisors()
	if err != nil {
		log.Error(err, "failed to get hypervisors")
		return
	}

	reservations := &v1alpha1.ReservationList{}
	if err := k.Client.List(context.Background(), reservations); err != nil {
		log.Error(err, "failed to list reservations")
		return
	}

	// This KPI only accounts for ready Nova reservations, grouped by Status.Host. The
	// shared capacity math is domain- and readiness-agnostic and keys off both TargetHost
	// and Status.Host, so filter to ready Nova reservations and pin TargetHost to
	// Status.Host — this reproduces the KPI's historical "committed slot minus confirmed
	// VMs" accounting (UnusedReservationCapacity only nets out confirmed VMs when
	// TargetHost == Status.Host, i.e. not mid-migration).
	blocking := make([]v1alpha1.Reservation, 0, len(reservations.Items))
	for _, r := range novaReservations(reservations.Items) {
		if !r.IsReady() || r.Status.Host == "" {
			continue
		}
		r.Spec.TargetHost = r.Status.Host
		blocking = append(blocking, r)
	}

	for _, hypervisor := range hypervisors {
		cpuTotal, hasCPUTotal := hypervisor.getResourceCapacity(hv1.ResourceCPU)

		ramTotal, hasRAMTotal := hypervisor.getResourceCapacity(hv1.ResourceMemory)

		if !hasCPUTotal || !hasRAMTotal {
			log.V(1).Info("hypervisor missing cpu or ram capacity, skipping", "host", hypervisor.Name)
			continue
		}

		cpuUsed := hypervisor.getResourceAllocation(hv1.ResourceCPU)
		ramUsed := hypervisor.getResourceAllocation(hv1.ResourceMemory)

		// Reservation blocks for this host, split into committed-not-in-use and failover
		// by the shared capacity math (ClampToZero is irrelevant here — we clamp PAYG below).
		b, err := capacity.DescribeHostUsage(hypervisor.Hypervisor, blocking, capacity.Options{
			BlockUnplacedReservations: true,
		})
		if err != nil {
			// getResourceCapacity already confirmed cpu+ram above, so this is unreachable
			// in practice; skip defensively if the capacity source ever disagrees.
			log.V(1).Info("skipping hypervisor without capacity data", "host", hypervisor.Name)
			continue
		}
		cpuReserved := b.ReservedCommitted[hv1.ResourceCPU]
		ramReserved := b.ReservedCommitted[hv1.ResourceMemory]
		cpuFailover := b.ReservedFailover[hv1.ResourceCPU]
		ramFailover := b.ReservedFailover[hv1.ResourceMemory]

		labels := hypervisor.getHostLabels()

		ch <- prometheus.MustNewConstMetric(k.totalCapacityPerHost, prometheus.GaugeValue, cpuTotal.AsApproximateFloat64(), append(labels, "cpu")...)
		ch <- prometheus.MustNewConstMetric(k.totalCapacityPerHost, prometheus.GaugeValue, ramTotal.AsApproximateFloat64(), append(labels, "ram")...)

		ch <- prometheus.MustNewConstMetric(k.capacityPerHost, prometheus.GaugeValue, cpuUsed.AsApproximateFloat64(), append(labels, "cpu", "utilized")...)
		ch <- prometheus.MustNewConstMetric(k.capacityPerHost, prometheus.GaugeValue, ramUsed.AsApproximateFloat64(), append(labels, "ram", "utilized")...)

		ch <- prometheus.MustNewConstMetric(k.capacityPerHost, prometheus.GaugeValue, cpuReserved.AsApproximateFloat64(), append(labels, "cpu", "reserved")...)
		ch <- prometheus.MustNewConstMetric(k.capacityPerHost, prometheus.GaugeValue, ramReserved.AsApproximateFloat64(), append(labels, "ram", "reserved")...)

		ch <- prometheus.MustNewConstMetric(k.capacityPerHost, prometheus.GaugeValue, cpuFailover.AsApproximateFloat64(), append(labels, "cpu", "failover")...)
		ch <- prometheus.MustNewConstMetric(k.capacityPerHost, prometheus.GaugeValue, ramFailover.AsApproximateFloat64(), append(labels, "ram", "failover")...)

		// Calculate PAYG capacity
		paygCPU := cpuTotal.DeepCopy()
		paygCPU.Sub(cpuUsed)
		paygCPU.Sub(cpuReserved)
		paygCPU.Sub(cpuFailover)
		if paygCPU.Cmp(resource.MustParse("0")) < 0 {
			paygCPU = resource.MustParse("0")
		}

		paygRAM := ramTotal.DeepCopy()
		paygRAM.Sub(ramUsed)
		paygRAM.Sub(ramReserved)
		paygRAM.Sub(ramFailover)
		if paygRAM.Cmp(resource.MustParse("0")) < 0 {
			paygRAM = resource.MustParse("0")
		}

		ch <- prometheus.MustNewConstMetric(k.capacityPerHost, prometheus.GaugeValue, paygCPU.AsApproximateFloat64(), append(labels, "cpu", "available")...)
		ch <- prometheus.MustNewConstMetric(k.capacityPerHost, prometheus.GaugeValue, paygRAM.AsApproximateFloat64(), append(labels, "ram", "available")...)
	}
}

func init() {
	plugins.Index["kvm_host_capacity_kpi"] = func() plugins.KPI { return &KVMHostCapacityKPI{} }
}
