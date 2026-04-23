// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package compute

import (
	"context"
	"log/slog"
	"strconv"
	"strings"

	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/cobaltcore-dev/cortex/api/v1alpha1"
	"github.com/cobaltcore-dev/cortex/internal/knowledge/db"
	"github.com/cobaltcore-dev/cortex/internal/knowledge/kpis/plugins"
	"github.com/cobaltcore-dev/cortex/pkg/conf"
	"github.com/prometheus/client_golang/prometheus"

	hv1 "github.com/cobaltcore-dev/openstack-hypervisor-operator/api/v1"
)

// Assuming hypervisor names are in the format nodeXXX-bbYY
func getBuildingBlock(hostName string) string {
	parts := strings.Split(hostName, "-")
	if len(parts) > 1 {
		return parts[1]
	}
	return "unknown"
}

// hostReservationResources holds aggregated CPU and memory reservation quantities for a single hypervisor.
type hostReservationResources struct {
	cpu    resource.Quantity
	memory resource.Quantity
}

type KVMResourceCapacityKPI struct {
	// Common base for all KPIs that provides standard functionality.
	plugins.BaseKPI[struct{}] // No options passed through yaml config
	totalCapacityPerHost      *prometheus.Desc
	capacityPerHost           *prometheus.Desc
}

func (KVMResourceCapacityKPI) GetName() string {
	return "kvm_host_capacity_kpi"
}

func (k *KVMResourceCapacityKPI) Init(db *db.DB, client client.Client, opts conf.RawOpts) error {
	if err := k.BaseKPI.Init(db, client, opts); err != nil {
		return err
	}
	k.totalCapacityPerHost = prometheus.NewDesc(
		"cortex_kvm_host_capacity_total",
		"Total resource capacity on the KVM hosts (individually by host).",
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
			"maintenance",
		},
		nil,
	)
	k.capacityPerHost = prometheus.NewDesc(
		"cortex_kvm_host_capacity_usage",
		"Resource capacity usage on the KVM hosts (individually by host).",
		[]string{
			"compute_host",
			"resource",
			"type",
			"availability_zone",
			"building_block",
			"cpu_architecture",
			"workload_type",
			"enabled",
			"decommissioned",
			"external_customer",
			"maintenance",
		},
		nil,
	)
	return nil
}

func (k *KVMResourceCapacityKPI) Describe(ch chan<- *prometheus.Desc) {
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

		readyCondition := meta.FindStatusCondition(reservation.Status.Conditions, v1alpha1.ReservationConditionReady)
		if readyCondition == nil || readyCondition.Status != metav1.ConditionTrue {
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

func (k *KVMResourceCapacityKPI) Collect(ch chan<- prometheus.Metric) {
	hvs := &hv1.HypervisorList{}
	if err := k.Client.List(context.Background(), hvs); err != nil {
		slog.Error("failed to list hypervisors", "error", err)
		return
	}

	reservations := &v1alpha1.ReservationList{}
	if err := k.Client.List(context.Background(), reservations); err != nil {
		slog.Error("failed to list reservations", "error", err)
		return
	}

	failoverByHost, committedNotInUseByHost := aggregateReservationsByHost(reservations.Items)

	for _, hypervisor := range hvs.Items {
		capacityMap := hypervisor.Status.EffectiveCapacity
		if capacityMap == nil {
			slog.Warn("hypervisor with nil effective capacity, falling back to physical capacity (overcommit not considered)", "host", hypervisor.Name)
			capacityMap = hypervisor.Status.Capacity
		}
		if capacityMap == nil {
			slog.Warn("hypervisor with nil capacity, skipping", "host", hypervisor.Name)
			continue
		}

		cpuTotal, hasCPUTotal := capacityMap[hv1.ResourceCPU]
		ramTotal, hasRAMTotal := capacityMap[hv1.ResourceMemory]

		if !hasCPUTotal || !hasRAMTotal {
			slog.Error("hypervisor missing cpu or ram total capacity", "hypervisor", hypervisor.Name)
			continue
		}

		if cpuTotal.IsZero() || ramTotal.IsZero() {
			slog.Warn("hypervisor with zero effective capacity, falling back to physical capacity (overcommit not considered)", "host", hypervisor.Name)
			if hypervisor.Status.Capacity == nil {
				slog.Warn("hypervisor with nil physical capacity, skipping", "host", hypervisor.Name)
				continue
			}
			cpuTotal = hypervisor.Status.Capacity[hv1.ResourceCPU]
			ramTotal = hypervisor.Status.Capacity[hv1.ResourceMemory]
			if cpuTotal.IsZero() || ramTotal.IsZero() {
				slog.Warn("hypervisor with zero physical capacity, skipping", "host", hypervisor.Name)
				continue
			}
		}

		cpuUsed, hasCPUUtilized := hypervisor.Status.Allocation[hv1.ResourceCPU]
		if !hasCPUUtilized {
			cpuUsed = resource.MustParse("0")
		}

		ramUsed, hasRAMUtilized := hypervisor.Status.Allocation[hv1.ResourceMemory]
		if !hasRAMUtilized {
			ramUsed = resource.MustParse("0")
		}

		// Get reservation data for this hypervisor (zero-value if absent).
		failoverRes := failoverByHost[hypervisor.Name]
		committedRes := committedNotInUseByHost[hypervisor.Name]

		cpuReserved := committedRes.cpu
		ramReserved := committedRes.memory
		cpuFailover := failoverRes.cpu
		ramFailover := failoverRes.memory

		labels := hostLabelsFromHypervisor(hypervisor)

		k.emitTotal(ch, "cpu", cpuTotal.AsApproximateFloat64(), labels)
		k.emitTotal(ch, "ram", ramTotal.AsApproximateFloat64(), labels)

		k.emitUsage(ch, "cpu", cpuUsed.AsApproximateFloat64(), "utilized", labels)
		k.emitUsage(ch, "ram", ramUsed.AsApproximateFloat64(), "utilized", labels)

		k.emitUsage(ch, "cpu", cpuReserved.AsApproximateFloat64(), "reserved", labels)
		k.emitUsage(ch, "ram", ramReserved.AsApproximateFloat64(), "reserved", labels)

		k.emitUsage(ch, "cpu", cpuFailover.AsApproximateFloat64(), "failover", labels)
		k.emitUsage(ch, "ram", ramFailover.AsApproximateFloat64(), "failover", labels)

		// Calculate PAYG capacity
		paygCPU := cpuTotal.DeepCopy()
		paygCPU.Sub(cpuUsed)
		paygCPU.Sub(cpuReserved)
		paygCPU.Sub(cpuFailover)

		paygRAM := ramTotal.DeepCopy()
		paygRAM.Sub(ramUsed)
		paygRAM.Sub(ramReserved)
		paygRAM.Sub(ramFailover)

		k.emitUsage(ch, "cpu", paygCPU.AsApproximateFloat64(), "payg", labels)
		k.emitUsage(ch, "ram", paygRAM.AsApproximateFloat64(), "payg", labels)
	}
}

// kvmHostLabels holds precomputed label values derived from a hypervisor.
type kvmHostLabels struct {
	computeHost      string
	availabilityZone string
	buildingBlock    string
	cpuArchitecture  string
	workloadType     string
	enabled          string
	decommissioned   string
	externalCustomer string
	maintenance      string
}

func hostLabelsFromHypervisor(hypervisor hv1.Hypervisor) kvmHostLabels {
	decommissioned := false
	externalCustomer := false
	workloadType := "general-purpose"
	cpuArchitecture := "cascade-lake"

	for _, trait := range hypervisor.Status.Traits {
		switch trait {
		case "CUSTOM_HW_SAPPHIRE_RAPIDS":
			cpuArchitecture = "sapphire-rapids"
		case "CUSTOM_HANA_EXCLUSIVE_HOST":
			workloadType = "hana"
		case "CUSTOM_DECOMMISSIONING":
			decommissioned = true
		case "CUSTOM_EXTERNAL_CUSTOMER_EXCLUSIVE":
			externalCustomer = true
		}
	}

	return kvmHostLabels{
		computeHost:      hypervisor.Name,
		availabilityZone: hypervisor.Labels["topology.kubernetes.io/zone"],
		buildingBlock:    getBuildingBlock(hypervisor.Name),
		cpuArchitecture:  cpuArchitecture,
		workloadType:     workloadType,
		enabled:          strconv.FormatBool(true),
		decommissioned:   strconv.FormatBool(decommissioned),
		externalCustomer: strconv.FormatBool(externalCustomer),
		maintenance:      strconv.FormatBool(false),
	}
}

func (k *KVMResourceCapacityKPI) emitTotal(ch chan<- prometheus.Metric, resourceName string, value float64, l kvmHostLabels) {
	ch <- prometheus.MustNewConstMetric(
		k.totalCapacityPerHost,
		prometheus.GaugeValue,
		value,
		l.computeHost,
		resourceName,
		l.availabilityZone,
		l.buildingBlock,
		l.cpuArchitecture,
		l.workloadType,
		l.enabled,
		l.decommissioned,
		l.externalCustomer,
		l.maintenance,
	)
}

func (k *KVMResourceCapacityKPI) emitUsage(ch chan<- prometheus.Metric, resourceName string, value float64, capacityType string, l kvmHostLabels) {
	ch <- prometheus.MustNewConstMetric(
		k.capacityPerHost,
		prometheus.GaugeValue,
		value,
		l.computeHost,
		resourceName,
		capacityType,
		l.availabilityZone,
		l.buildingBlock,
		l.cpuArchitecture,
		l.workloadType,
		l.enabled,
		l.decommissioned,
		l.externalCustomer,
		l.maintenance,
	)
}
