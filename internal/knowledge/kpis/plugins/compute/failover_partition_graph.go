// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package compute

import (
	"context"
	"time"

	"github.com/cobaltcore-dev/cortex/api/v1alpha1"
	"github.com/cobaltcore-dev/cortex/internal/knowledge/db"
	"github.com/cobaltcore-dev/cortex/internal/knowledge/kpis/plugins"
	"github.com/cobaltcore-dev/cortex/pkg/conf"
	hv1 "github.com/cobaltcore-dev/openstack-hypervisor-operator/api/v1"
	"github.com/prometheus/client_golang/prometheus"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

var failoverPartitionGraphKPILogger = ctrl.Log.WithName("failover-partition-graph-kpi")

// KPI exposing the failover partition graph derived from failover host
// reservations. The graph has two series:
//
//   - cortex_failover_partition_graph_node{host,az,memory,cpu} 1 — one per host
//     participating in the graph: every reserved failover host (the TargetHost
//     of a failover host reservation) and every SourceHost that may fail over
//     onto it. Source hosts carry the same availability zone and reserved
//     capacity as the target they fail over onto.
//   - cortex_failover_partition_graph_edge{source,target} 1 — one per directed
//     evacuation path: every SourceHost of a reservation may fail over onto the
//     reservation's TargetHost.
//
// The graph is fully recoverable from the reservation specs, so it lives here
// as a KPI rather than in the failover host controller.
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
		"A reserved failover host in the failover partition graph",
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

// Collect the failover partition graph metrics from the failover host
// reservations present in the cluster.
func (k *FailoverPartitionGraphKPI) Collect(ch chan<- prometheus.Metric) {
	// Bound the list call so a slow API server can't hang the Prometheus scrape
	// indefinitely; if it fails we log so the disappearance of the metric is not
	// silent.
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	reservationList := &v1alpha1.ReservationList{}
	if err := k.Client.List(ctx, reservationList); err != nil {
		failoverPartitionGraphKPILogger.Error(err, "Failed to list reservations")
		return
	}
	// Dedup nodes and edges across reservations: a target host appears once, and
	// a (source,target) pair could in principle repeat. Prometheus rejects
	// duplicate series within a single scrape, so collapse them into sets first.
	// Every host participating in the graph is a node. Source hosts carry the
	// same az and reserved capacity as the target they fail over onto, but a host
	// that is itself a target must keep its own target metadata. We therefore
	// emit all target nodes and edges first, recording each source's inherited
	// metadata, then emit the source hosts that are not targets.
	targets := map[string]struct{}{}
	type edge struct{ source, target string }
	emittedEdges := map[edge]struct{}{}
	type nodeMeta struct{ az, memory, cpu string }
	sources := map[string]nodeMeta{}
	for _, r := range reservationList.Items {
		if r.Spec.Type != v1alpha1.ReservationTypeFailoverHost {
			continue
		}
		target := r.Spec.TargetHost
		if target == "" {
			continue
		}
		memory := r.Spec.Resources[hv1.ResourceMemory]
		cpu := r.Spec.Resources[hv1.ResourceCPU]
		if _, ok := targets[target]; !ok {
			targets[target] = struct{}{}
			ch <- prometheus.MustNewConstMetric(
				k.node, prometheus.GaugeValue, 1,
				target, r.Spec.AvailabilityZone, memory.String(), cpu.String(),
			)
		}
		if r.Spec.FailoverHostReservation == nil {
			continue
		}
		for _, source := range r.Spec.FailoverHostReservation.SourceHosts {
			if _, ok := sources[source]; !ok {
				sources[source] = nodeMeta{r.Spec.AvailabilityZone, memory.String(), cpu.String()}
			}
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
	// Source hosts that are not themselves targets become nodes carrying the
	// az and capacity of the failover target they belong to.
	for source, meta := range sources {
		if _, ok := targets[source]; ok {
			continue
		}
		ch <- prometheus.MustNewConstMetric(
			k.node, prometheus.GaugeValue, 1, source, meta.az, meta.memory, meta.cpu,
		)
	}
}
