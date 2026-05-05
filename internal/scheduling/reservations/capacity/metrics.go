// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package capacity

import (
	"context"

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
	client               client.Client
	totalCapacityVMSlots *prometheus.GaugeVec
	placeableVMs         *prometheus.GaugeVec
	totalCapacityHosts   *prometheus.GaugeVec
	placeableHosts       *prometheus.GaugeVec
	totalInstances       *prometheus.GaugeVec
	committedCapacity    *prometheus.GaugeVec
}

// NewMonitor creates a new Monitor that reads FlavorGroupCapacity CRDs.
func NewMonitor(c client.Client) Monitor {
	return Monitor{
		client: c,
		totalCapacityVMSlots: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name: "cortex_committed_resource_capacity_total",
			Help: "Total schedulable slots in an empty-datacenter scenario per flavor.",
		}, capacityFlavorLabels),
		placeableVMs: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name: "cortex_committed_resource_capacity_placeable",
			Help: "Schedulable slots remaining given current VM allocations per flavor.",
		}, capacityFlavorLabels),
		totalCapacityHosts: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name: "cortex_committed_resource_capacity_hosts_total",
			Help: "Number of hosts eligible for this flavor in the empty-state probe.",
		}, capacityFlavorLabels),
		placeableHosts: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name: "cortex_committed_resource_capacity_hosts_placeable",
			Help: "Number of hosts still able to accept a new VM of this flavor.",
		}, capacityFlavorLabels),
		totalInstances: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name: "cortex_committed_resource_capacity_instances",
			Help: "Total VM instances running on hypervisors in this AZ (not filtered by flavor group).",
		}, capacityLabels),
		committedCapacity: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name: "cortex_committed_resource_capacity_committed",
			Help: "Sum of AcceptedAmount across Ready CommittedResource CRDs for this flavor group and AZ.",
		}, capacityLabels),
	}
}

// Describe implements prometheus.Collector.
func (m *Monitor) Describe(ch chan<- *prometheus.Desc) {
	m.totalCapacityVMSlots.Describe(ch)
	m.placeableVMs.Describe(ch)
	m.totalCapacityHosts.Describe(ch)
	m.placeableHosts.Describe(ch)
	m.totalInstances.Describe(ch)
	m.committedCapacity.Describe(ch)
}

// Collect implements prometheus.Collector — lists all FlavorGroupCapacity CRDs and exports gauges.
func (m *Monitor) Collect(ch chan<- prometheus.Metric) {
	var list v1alpha1.FlavorGroupCapacityList
	if err := m.client.List(context.Background(), &list); err != nil {
		log.Error(err, "failed to list FlavorGroupCapacity CRDs for metrics")
		return
	}

	// Reset all gauges so deleted CRDs don't linger.
	m.totalCapacityVMSlots.Reset()
	m.placeableVMs.Reset()
	m.totalCapacityHosts.Reset()
	m.placeableHosts.Reset()
	m.totalInstances.Reset()
	m.committedCapacity.Reset()

	for _, crd := range list.Items {
		groupAZLabels := prometheus.Labels{
			"flavor_group": crd.Spec.FlavorGroup,
			"az":           crd.Spec.AvailabilityZone,
		}
		m.totalInstances.With(groupAZLabels).Set(float64(crd.Status.TotalInstances))
		m.committedCapacity.With(groupAZLabels).Set(float64(crd.Status.CommittedCapacity))

		for _, f := range crd.Status.Flavors {
			flavorLabels := prometheus.Labels{
				"flavor_group": crd.Spec.FlavorGroup,
				"az":           crd.Spec.AvailabilityZone,
				"flavor_name":  f.FlavorName,
			}
			m.totalCapacityVMSlots.With(flavorLabels).Set(float64(f.TotalCapacityVMSlots))
			m.placeableVMs.With(flavorLabels).Set(float64(f.PlaceableVMs))
			m.totalCapacityHosts.With(flavorLabels).Set(float64(f.TotalCapacityHosts))
			m.placeableHosts.With(flavorLabels).Set(float64(f.PlaceableHosts))
		}
	}

	m.totalCapacityVMSlots.Collect(ch)
	m.placeableVMs.Collect(ch)
	m.totalCapacityHosts.Collect(ch)
	m.placeableHosts.Collect(ch)
	m.totalInstances.Collect(ch)
	m.committedCapacity.Collect(ch)
}
