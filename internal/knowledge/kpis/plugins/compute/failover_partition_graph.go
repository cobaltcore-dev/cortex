// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package compute

import (
	"context"
	"maps"
	"time"

	"github.com/cobaltcore-dev/cortex/api/v1alpha1"
	"github.com/cobaltcore-dev/cortex/internal/knowledge/db"
	"github.com/cobaltcore-dev/cortex/internal/knowledge/kpis/plugins"
	"github.com/cobaltcore-dev/cortex/pkg/conf"
	hv1 "github.com/cobaltcore-dev/openstack-hypervisor-operator/api/v1"
	"github.com/prometheus/client_golang/prometheus"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

var failoverPartitionGraphKPILogger = ctrl.Log.WithName("failover-partition-graph-kpi")

// KPI exposing the failover partition graph derived from the hypervisors and
// failover host reservations present in the cluster. The graph has two series:
//
//   - cortex_failover_partition_graph_node{host,az,memory,cpu} — one per
//     hypervisor, whether or not it participates in any failover reservation,
//     carrying the hypervisor's availability zone and total capacity. The value
//     is 1 when the hypervisor has at least one failover path (it is the source
//     or target of an edge) and 0 when it has none.
//   - cortex_failover_partition_graph_edge{source,target} 1 — one per directed
//     evacuation path: every SourceHost of a failover host reservation may fail
//     over onto the reservation's TargetHost.
type FailoverPartitionGraphKPI struct {
	// Common base for all KPIs that provides standard functionality.
	plugins.BaseKPI[struct{}]

	node *prometheus.Desc
	edge *prometheus.Desc
}

func (FailoverPartitionGraphKPI) GetName() string { return "failover_partition_graph_kpi" }

// Initialize the KPI.
func (k *FailoverPartitionGraphKPI) Init(db *db.DB, client client.Client, opts conf.RawOpts) error {
	if err := k.BaseKPI.Init(db, client, opts); err != nil {
		return err
	}
	k.node = prometheus.NewDesc(
		"cortex_failover_partition_graph_node",
		"A hypervisor in the failover partition graph",
		[]string{"host", "az", "memory", "cpu"},
		nil,
	)
	k.edge = prometheus.NewDesc(
		"cortex_failover_partition_graph_edge",
		"A directed evacuation path (source can fail over onto target) in the failover partition graph",
		[]string{"source", "target"},
		nil,
	)
	return nil
}

// Conform to the prometheus collector interface by providing the descriptors.
func (k *FailoverPartitionGraphKPI) Describe(ch chan<- *prometheus.Desc) {
	ch <- k.node
	ch <- k.edge
}

// Collect the failover partition graph metrics: one node per hypervisor and one
// edge per failover evacuation path.
func (k *FailoverPartitionGraphKPI) Collect(ch chan<- prometheus.Metric) {
	// Bound the list calls so a slow API server can't hang the Prometheus scrape
	// indefinitely; if they fail we log so the disappearance of the metrics is
	// not silent.
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	reservationList := &v1alpha1.ReservationList{}
	if err := k.Client.List(ctx, reservationList); err != nil {
		failoverPartitionGraphKPILogger.Error(err, "Failed to list reservations")
		return
	}
	// A (source,target) pair could in principle repeat across reservations, and
	// Prometheus rejects duplicate series within a single scrape, so collapse the
	// edges into a set first. Track which hosts take part in any path so nodes
	// without a failover path can be reported with value 0.
	type edge struct{ source, target string }
	emittedEdges := map[edge]struct{}{}
	hasPath := map[string]struct{}{}
	for _, r := range reservationList.Items {
		if r.Spec.Type != v1alpha1.ReservationTypeFailoverHost {
			continue
		}
		target := r.Spec.TargetHost
		if target == "" || r.Spec.FailoverHostReservation == nil {
			continue
		}
		for _, source := range r.Spec.FailoverHostReservation.SourceHosts {
			hasPath[source] = struct{}{}
			hasPath[target] = struct{}{}
			e := edge{source: source, target: target}
			if _, ok := emittedEdges[e]; ok {
				continue
			}
			emittedEdges[e] = struct{}{}
			ch <- prometheus.MustNewConstMetric(
				k.edge, prometheus.GaugeValue, 1, source, target,
			)
		}
	}
	hypervisorList := &hv1.HypervisorList{}
	if err := k.Client.List(ctx, hypervisorList); err != nil {
		failoverPartitionGraphKPILogger.Error(err, "Failed to list hypervisors")
		return
	}
	for _, hv := range hypervisorList.Items {
		capacity := map[hv1.ResourceName]resource.Quantity{}
		maps.Copy(capacity, hv.Status.Capacity) // base capacity
		maps.Copy(capacity, hv.Status.EffectiveCapacity)
		memory := capacity[hv1.ResourceMemory]
		cpu := capacity[hv1.ResourceCPU]
		value := 0.0
		if _, ok := hasPath[hv.Name]; ok {
			value = 1
		}
		ch <- prometheus.MustNewConstMetric(
			k.node, prometheus.GaugeValue, value,
			hv.Name, hv.Labels[corev1.LabelTopologyZone], memory.String(), cpu.String(),
		)
	}
}
