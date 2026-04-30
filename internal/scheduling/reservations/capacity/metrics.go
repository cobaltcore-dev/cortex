// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package capacity

import (
	"context"

	"github.com/cobaltcore-dev/cortex/api/v1alpha1"
	"github.com/prometheus/client_golang/prometheus"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

var capacityLabels = []string{"flavor_group", "az"}

// Monitor provides Prometheus metrics for FlavorGroupCapacity CRDs.
// It implements prometheus.Collector and reads CRD status on each Collect call.
type Monitor struct {
	client            client.Client
	totalCapacity     *prometheus.GaugeVec
	totalPlaceable    *prometheus.GaugeVec
	totalHosts        *prometheus.GaugeVec
	placeableHosts    *prometheus.GaugeVec
	totalInstances    *prometheus.GaugeVec
	committedCapacity *prometheus.GaugeVec
}

// NewMonitor creates a new Monitor that reads FlavorGroupCapacity CRDs.
func NewMonitor(c client.Client) Monitor {
	return Monitor{
		client: c,
		totalCapacity: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name: "cortex_committed_resource_capacity_total",
			Help: "Total schedulable slots in an empty-datacenter scenario per flavor group and AZ.",
		}, capacityLabels),
		totalPlaceable: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name: "cortex_committed_resource_capacity_placeable",
			Help: "Schedulable slots remaining given current VM allocations per flavor group and AZ.",
		}, capacityLabels),
		totalHosts: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name: "cortex_committed_resource_capacity_hosts_total",
			Help: "Number of hosts eligible for this flavor group in the empty-state probe.",
		}, capacityLabels),
		placeableHosts: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name: "cortex_committed_resource_capacity_hosts_placeable",
			Help: "Number of hosts still able to accept a new smallest-flavor VM.",
		}, capacityLabels),
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
	m.totalCapacity.Describe(ch)
	m.totalPlaceable.Describe(ch)
	m.totalHosts.Describe(ch)
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
	m.totalCapacity.Reset()
	m.totalPlaceable.Reset()
	m.totalHosts.Reset()
	m.placeableHosts.Reset()
	m.totalInstances.Reset()
	m.committedCapacity.Reset()

	for _, c := range list.Items {
		labels := prometheus.Labels{
			"flavor_group": c.Spec.FlavorGroup,
			"az":           c.Spec.AvailabilityZone,
		}
		m.totalCapacity.With(labels).Set(float64(c.Status.TotalCapacity))
		m.totalPlaceable.With(labels).Set(float64(c.Status.TotalPlaceable))
		m.totalHosts.With(labels).Set(float64(c.Status.TotalHosts))
		m.placeableHosts.With(labels).Set(float64(c.Status.PlaceableHosts))
		m.totalInstances.With(labels).Set(float64(c.Status.TotalInstances))
		m.committedCapacity.With(labels).Set(float64(c.Status.CommittedCapacity))
	}

	m.totalCapacity.Collect(ch)
	m.totalPlaceable.Collect(ch)
	m.totalHosts.Collect(ch)
	m.placeableHosts.Collect(ch)
	m.totalInstances.Collect(ch)
	m.committedCapacity.Collect(ch)
}
