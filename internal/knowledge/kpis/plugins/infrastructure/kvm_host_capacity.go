// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package infrastructure

import (
	"context"
	"log/slog"

	"k8s.io/apimachinery/pkg/api/resource"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/cobaltcore-dev/cortex/api/v1alpha1"
	"github.com/cobaltcore-dev/cortex/internal/knowledge/db"
	"github.com/cobaltcore-dev/cortex/internal/knowledge/kpis/plugins"
	"github.com/cobaltcore-dev/cortex/pkg/conf"
	"github.com/prometheus/client_golang/prometheus"

	hv1 "github.com/cobaltcore-dev/openstack-hypervisor-operator/api/v1"
)

// hostReservationResources holds aggregated CPU and memory reservation quantities for a single hypervisor.
type hostReservationResources struct {
	cpu    resource.Quantity
	memory resource.Quantity
}

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

// aggregateReservationsByHost groups Ready reservations by host, returning per-host
// failover totals and committed-resource "not yet in use" totals.
func aggregateReservationsByHost(reservations []v1alpha1.Reservation) (
	failoverByHost map[string]hostReservationResources,
	committedNotInUseByHost map[string]hostReservationResources,
) {

	failoverByHost = make(map[string]hostReservationResources)
	committedNotInUseByHost = make(map[string]hostReservationResources)

	for _, reservation := range reservations {
		if reservation.Spec.SchedulingDomain != v1alpha1.SchedulingDomainNova {
			continue
		}

		if !reservation.IsReady() {
			continue
		}

		host := reservation.Status.Host
		if host == "" {
			continue
		}

		switch reservation.Spec.Type {
		case v1alpha1.ReservationTypeFailover:
			entry := failoverByHost[host]
			cpuQty := reservation.Spec.Resources[hv1.ResourceCPU]
			entry.cpu.Add(cpuQty)
			memQty := reservation.Spec.Resources[hv1.ResourceMemory]
			entry.memory.Add(memQty)
			failoverByHost[host] = entry

		case v1alpha1.ReservationTypeCommittedResource:
			// Total reserved resources for this reservation.
			cpuTotal := reservation.Spec.Resources[hv1.ResourceCPU]
			memTotal := reservation.Spec.Resources[hv1.ResourceMemory]

			// Sum allocated resources across all workloads.
			var cpuAllocated, memAllocated resource.Quantity
			if reservation.Spec.CommittedResourceReservation != nil {
				for _, alloc := range reservation.Spec.CommittedResourceReservation.Allocations {
					cpuAllocated.Add(alloc.Resources[hv1.ResourceCPU])
					memAllocated.Add(alloc.Resources[hv1.ResourceMemory])
				}
			}

			// Not yet in use = total - allocated, clamped to zero.
			cpuNotInUse := cpuTotal.DeepCopy()
			cpuNotInUse.Sub(cpuAllocated)
			if cpuNotInUse.Cmp(resource.MustParse("0")) < 0 {
				cpuNotInUse = resource.MustParse("0")
			}

			memNotInUse := memTotal.DeepCopy()
			memNotInUse.Sub(memAllocated)
			if memNotInUse.Cmp(resource.MustParse("0")) < 0 {
				memNotInUse = resource.MustParse("0")
			}

			entry := committedNotInUseByHost[host]
			entry.cpu.Add(cpuNotInUse)
			entry.memory.Add(memNotInUse)
			committedNotInUseByHost[host] = entry
		}
	}

	return failoverByHost, committedNotInUseByHost
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
		slog.Error("failed to get hypervisors", "error", err)
		return
	}

	reservations := &v1alpha1.ReservationList{}
	if err := k.Client.List(context.Background(), reservations); err != nil {
		slog.Error("failed to list reservations", "error", err)
		return
	}

	failoverByHost, committedNotInUseByHost := aggregateReservationsByHost(reservations.Items)

	for _, hypervisor := range hypervisors {
		cpuTotal, hasCPUTotal := hypervisor.getResourceCapacity(hv1.ResourceCPU)

		ramTotal, hasRAMTotal := hypervisor.getResourceCapacity(hv1.ResourceMemory)

		if !hasCPUTotal || !hasRAMTotal {
			slog.Warn("hypervisor missing cpu or ram capacity, skipping", "host", hypervisor.Name)
			continue
		}

		cpuUsed := hypervisor.getResourceAllocation(hv1.ResourceCPU)
		ramUsed := hypervisor.getResourceAllocation(hv1.ResourceMemory)

		// Get reservation data for this hypervisor (zero-value if absent).
		failoverRes := failoverByHost[hypervisor.Name]
		committedRes := committedNotInUseByHost[hypervisor.Name]

		cpuReserved := committedRes.cpu
		ramReserved := committedRes.memory

		cpuFailover := failoverRes.cpu
		ramFailover := failoverRes.memory

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
