// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package capacity

import (
	"context"
	"time"

	"github.com/cobaltcore-dev/cortex/api/v1alpha1"
	"github.com/prometheus/client_golang/prometheus"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

var (
	capacityLabels       = []string{"flavor_group", "az"}
	capacityFlavorLabels = []string{"flavor_group", "az", "flavor_name"}
)

// Monitor provides Prometheus metrics for FlavorGroupCapacity CRDs.
// It implements prometheus.Collector and reads CRD status on each Collect call.
type Monitor struct {
	client            client.Client
	vmSlotsEmpty      *prometheus.GaugeVec
	vmSlotsPlaceable  *prometheus.GaugeVec
	hostsEmpty        *prometheus.GaugeVec
	hostsPlaceable    *prometheus.GaugeVec
	committedCapacity *prometheus.GaugeVec
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
		committedCapacity: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name: "cortex_committed_resource_committed_gib",
			Help: "Sum of AcceptedAmount in GiB across Ready CommittedResource CRDs for this flavor group and AZ.",
		}, capacityLabels),
	}
}

// Describe implements prometheus.Collector.
func (m *Monitor) Describe(ch chan<- *prometheus.Desc) {
	m.vmSlotsEmpty.Describe(ch)
	m.vmSlotsPlaceable.Describe(ch)
	m.hostsEmpty.Describe(ch)
	m.hostsPlaceable.Describe(ch)
	m.committedCapacity.Describe(ch)
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
	m.committedCapacity.Reset()

	for _, crd := range list.Items {
		groupAZLabels := prometheus.Labels{
			"flavor_group": crd.Spec.FlavorGroup,
			"az":           crd.Spec.AvailabilityZone,
		}
		m.committedCapacity.With(groupAZLabels).Set(float64(crd.Status.CommittedCapacity))

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
	m.committedCapacity.Collect(ch)
}
