// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package capacity

import (
	"context"
	"time"

	"github.com/cobaltcore-dev/cortex/api/v1alpha1"
	"github.com/prometheus/client_golang/prometheus"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

var (
	capacityLabels       = []string{"flavor_group", "az"}
	capacityFlavorLabels = []string{"flavor_group", "az", "flavor_name"}
)

// Monitor provides Prometheus metrics for FlavorGroupCapacity CRDs.
// It implements prometheus.Collector and reads CRD status on each Collect call.
type Monitor struct {
	client                            client.Client
	vmSlotsEmpty                      *prometheus.GaugeVec
	vmSlotsPlaceable                  *prometheus.GaugeVec
	hostsEmpty                        *prometheus.GaugeVec
	hostsPlaceable                    *prometheus.GaugeVec
	committedCapacityGiB              *prometheus.GaugeVec
	committedReservations             *prometheus.GaugeVec
	runningInstances                  *prometheus.GaugeVec
	freeCapacityGiB                   *prometheus.GaugeVec
	exclusivelyFreeCapacityGiB        *prometheus.GaugeVec
	exclusivelyRawCapacityGiB         *prometheus.GaugeVec
	exclusivelyFreeSlots              *prometheus.GaugeVec
	runningSlots                      *prometheus.GaugeVec
	exclusivelyCommittedReservedGiB   *prometheus.GaugeVec
	exclusivelyCommittedReservedSlots *prometheus.GaugeVec
	exclusivelyFailoverReservedGiB    *prometheus.GaugeVec
	exclusivelyFailoverReservedSlots  *prometheus.GaugeVec
	readyGauge                        *prometheus.GaugeVec
}

// NewMonitor creates a new Monitor that reads FlavorGroupCapacity CRDs.
func NewMonitor(c client.Client) Monitor {
	return Monitor{
		client: c,
		vmSlotsEmpty: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name: "cortex_committed_resource_capacity_vm_slots_empty_datacenter",
			Help: "Schedulable VM slots per flavor assuming an empty datacenter (no existing VMs).",
		}, capacityFlavorLabels),
		vmSlotsPlaceable: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name: "cortex_committed_resource_capacity_vm_slots_placeable",
			Help: "Schedulable VM slots remaining per flavor given current VM allocations.",
		}, capacityFlavorLabels),
		hostsEmpty: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name: "cortex_committed_resource_capacity_hosts_empty_datacenter",
			Help: "Number of hosts eligible for this flavor assuming an empty datacenter.",
		}, capacityFlavorLabels),
		hostsPlaceable: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name: "cortex_committed_resource_capacity_hosts_placeable",
			Help: "Number of hosts still able to accept a new VM of this flavor.",
		}, capacityFlavorLabels),
		committedCapacityGiB: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name: "cortex_committed_resource_committed_gib",
			Help: "Total committed memory in GiB for this flavor group and AZ.",
		}, capacityLabels),
		committedReservations: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name: "cortex_committed_resource_committed_reservations",
			Help: "Number of committed reservation slots (smallest-flavor units) for this flavor group and AZ.",
		}, capacityLabels),
		runningInstances: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name: "cortex_committed_resource_running_instances",
			Help: "Number of running VMs whose flavor belongs to this flavor group and AZ.",
		}, capacityFlavorLabels),
		freeCapacityGiB: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name: "cortex_committed_resource_free_capacity_gib",
			Help: "Sum of remaining memory in GiB across all candidate hosts for this flavor group before the cross-group split. May overlap across groups sharing hosts.",
		}, capacityFlavorLabels),
		exclusivelyFreeCapacityGiB: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name: "cortex_committed_resource_exclusively_free_capacity_gib",
			Help: "Memory in GiB fairly attributed to this flavor group by the round-robin split. Sum across groups never exceeds installed capacity.",
		}, capacityFlavorLabels),
		exclusivelyRawCapacityGiB: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name: "cortex_committed_resource_exclusively_raw_capacity_gib",
			Help: "Raw unquantized memory in GiB across hosts exclusively assigned to this group by the split. Not CPU-constrained; safe to sum across groups.",
		}, capacityFlavorLabels),
		exclusivelyFreeSlots: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name: "cortex_committed_resource_exclusively_free_slots",
			Help: "Number of smallest-flavor VM slots available after the cross-group capacity split.",
		}, capacityFlavorLabels),
		runningSlots: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name: "cortex_committed_resource_running_slots",
			Help: "Running VM consumption in smallest-flavor slot units. Unlike running_instances, a VM larger than the smallest flavor counts as the several slots it occupies.",
		}, capacityFlavorLabels),
		exclusivelyCommittedReservedGiB: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name: "cortex_committed_resource_exclusively_committed_reserved_capacity_gib",
			Help: "Memory in GiB held by unfilled committed-resource reservations. Included in reported capacity to Limes.",
		}, capacityFlavorLabels),
		exclusivelyCommittedReservedSlots: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name: "cortex_committed_resource_exclusively_committed_reserved_slots",
			Help: "Unfilled committed-resource reservation slots in smallest-flavor units. Included in reported capacity to Limes.",
		}, capacityFlavorLabels),
		exclusivelyFailoverReservedGiB: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name: "cortex_committed_resource_exclusively_failover_reserved_capacity_gib",
			Help: "Memory in GiB held by failover reservations (hardware held for host evacuation). Excluded from reported capacity to Limes.",
		}, capacityFlavorLabels),
		exclusivelyFailoverReservedSlots: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name: "cortex_committed_resource_exclusively_failover_reserved_slots",
			Help: "Failover reservation slots in smallest-flavor units. Excluded from reported capacity to Limes.",
		}, capacityFlavorLabels),
		readyGauge: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name: "cortex_committed_resource_capacity_ready",
			Help: "1 if the FlavorGroupCapacity CRD is Ready (all scheduler probes succeeded), 0 otherwise.",
		}, capacityLabels),
	}
}

// Describe implements prometheus.Collector.
func (m *Monitor) Describe(ch chan<- *prometheus.Desc) {
	m.vmSlotsEmpty.Describe(ch)
	m.vmSlotsPlaceable.Describe(ch)
	m.hostsEmpty.Describe(ch)
	m.hostsPlaceable.Describe(ch)
	m.committedCapacityGiB.Describe(ch)
	m.committedReservations.Describe(ch)
	m.runningInstances.Describe(ch)
	m.freeCapacityGiB.Describe(ch)
	m.exclusivelyFreeCapacityGiB.Describe(ch)
	m.exclusivelyRawCapacityGiB.Describe(ch)
	m.exclusivelyFreeSlots.Describe(ch)
	m.runningSlots.Describe(ch)
	m.exclusivelyCommittedReservedGiB.Describe(ch)
	m.exclusivelyCommittedReservedSlots.Describe(ch)
	m.exclusivelyFailoverReservedGiB.Describe(ch)
	m.exclusivelyFailoverReservedSlots.Describe(ch)
	m.readyGauge.Describe(ch)
}

// Collect implements prometheus.Collector — lists all FlavorGroupCapacity CRDs and exports gauges.
func (m *Monitor) Collect(ch chan<- prometheus.Metric) {
	var list v1alpha1.FlavorGroupCapacityList
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := m.client.List(ctx, &list); err != nil {
		log.Error(err, "failed to list FlavorGroupCapacity CRDs for metrics")
		return
	}

	// Reset all gauges so deleted CRDs don't linger.
	m.vmSlotsEmpty.Reset()
	m.vmSlotsPlaceable.Reset()
	m.hostsEmpty.Reset()
	m.hostsPlaceable.Reset()
	m.committedCapacityGiB.Reset()
	m.committedReservations.Reset()
	m.runningInstances.Reset()
	m.freeCapacityGiB.Reset()
	m.exclusivelyFreeCapacityGiB.Reset()
	m.exclusivelyRawCapacityGiB.Reset()
	m.exclusivelyFreeSlots.Reset()
	m.runningSlots.Reset()
	m.exclusivelyCommittedReservedGiB.Reset()
	m.exclusivelyCommittedReservedSlots.Reset()
	m.exclusivelyFailoverReservedGiB.Reset()
	m.exclusivelyFailoverReservedSlots.Reset()
	m.readyGauge.Reset()

	for _, crd := range list.Items {
		groupAZLabels := prometheus.Labels{
			"flavor_group": crd.Spec.FlavorGroup,
			"az":           crd.Spec.AvailabilityZone,
		}
		groupAZFlavorLabels := prometheus.Labels{
			"flavor_group": crd.Spec.FlavorGroup,
			"az":           crd.Spec.AvailabilityZone,
			"flavor_name":  crd.Status.SmallestFlavorName,
		}
		m.committedCapacityGiB.With(groupAZLabels).Set(float64(crd.Status.CommittedCapacityBytes) / (1024 * 1024 * 1024))
		m.committedReservations.With(groupAZLabels).Set(float64(crd.Status.CommittedCapacity))
		m.runningInstances.With(groupAZFlavorLabels).Set(float64(crd.Status.RunningInstances))

		if qty, ok := crd.Status.FreeCapacity[string(v1alpha1.CommittedResourceTypeMemory)]; ok {
			m.freeCapacityGiB.With(groupAZFlavorLabels).Set(float64(qty.Value()) / (1024 * 1024 * 1024))
		}
		if qty, ok := crd.Status.ExclusivelyFreeCapacity[string(v1alpha1.CommittedResourceTypeMemory)]; ok {
			m.exclusivelyFreeCapacityGiB.With(groupAZFlavorLabels).Set(float64(qty.Value()) / (1024 * 1024 * 1024))
		}
		if qty, ok := crd.Status.ExclusivelyRawCapacity[string(v1alpha1.CommittedResourceTypeMemory)]; ok {
			m.exclusivelyRawCapacityGiB.With(groupAZFlavorLabels).Set(float64(qty.Value()) / (1024 * 1024 * 1024))
		}
		m.exclusivelyFreeSlots.With(groupAZFlavorLabels).Set(float64(crd.Status.ExclusivelyFreeSlots))
		m.runningSlots.With(groupAZFlavorLabels).Set(float64(crd.Status.RunningSlots))
		if qty, ok := crd.Status.ExclusivelyCommittedReservedCapacity[string(v1alpha1.CommittedResourceTypeMemory)]; ok {
			m.exclusivelyCommittedReservedGiB.With(groupAZFlavorLabels).Set(float64(qty.Value()) / (1024 * 1024 * 1024))
		}
		m.exclusivelyCommittedReservedSlots.With(groupAZFlavorLabels).Set(float64(crd.Status.ExclusivelyCommittedReservedSlots))
		if qty, ok := crd.Status.ExclusivelyFailoverReservedCapacity[string(v1alpha1.CommittedResourceTypeMemory)]; ok {
			m.exclusivelyFailoverReservedGiB.With(groupAZFlavorLabels).Set(float64(qty.Value()) / (1024 * 1024 * 1024))
		}
		m.exclusivelyFailoverReservedSlots.With(groupAZFlavorLabels).Set(float64(crd.Status.ExclusivelyFailoverReservedSlots))

		readyVal := 0.0
		if apimeta.IsStatusConditionTrue(crd.Status.Conditions, v1alpha1.FlavorGroupCapacityConditionReady) {
			readyVal = 1.0
		}
		m.readyGauge.With(groupAZLabels).Set(readyVal)

		for _, f := range crd.Status.Flavors {
			flavorLabels := prometheus.Labels{
				"flavor_group": crd.Spec.FlavorGroup,
				"az":           crd.Spec.AvailabilityZone,
				"flavor_name":  f.FlavorName,
			}
			m.vmSlotsEmpty.With(flavorLabels).Set(float64(f.TotalCapacityVMSlots))
			m.vmSlotsPlaceable.With(flavorLabels).Set(float64(f.PlaceableVMs))
			m.hostsEmpty.With(flavorLabels).Set(float64(f.TotalCapacityHosts))
			m.hostsPlaceable.With(flavorLabels).Set(float64(f.PlaceableHosts))
		}
	}

	m.vmSlotsEmpty.Collect(ch)
	m.vmSlotsPlaceable.Collect(ch)
	m.hostsEmpty.Collect(ch)
	m.hostsPlaceable.Collect(ch)
	m.committedCapacityGiB.Collect(ch)
	m.committedReservations.Collect(ch)
	m.runningInstances.Collect(ch)
	m.freeCapacityGiB.Collect(ch)
	m.exclusivelyFreeCapacityGiB.Collect(ch)
	m.exclusivelyRawCapacityGiB.Collect(ch)
	m.exclusivelyFreeSlots.Collect(ch)
	m.runningSlots.Collect(ch)
	m.exclusivelyCommittedReservedGiB.Collect(ch)
	m.exclusivelyCommittedReservedSlots.Collect(ch)
	m.exclusivelyFailoverReservedGiB.Collect(ch)
	m.exclusivelyFailoverReservedSlots.Collect(ch)
	m.readyGauge.Collect(ch)
}
