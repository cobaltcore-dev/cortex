// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package failoverhost

import (
	"context"
	"sort"
	"time"

	"github.com/cobaltcore-dev/cortex/api/v1alpha1"
	hv1 "github.com/cobaltcore-dev/openstack-hypervisor-operator/api/v1"
	corev1 "k8s.io/api/core/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
)

// ReservationGenerator computes the full desired set of failover host
// reservations from the current cluster state. It is the swappable "producer"
// half of the controller: the controller's loop calls Generate each pass, swaps
// the result in as the desired state, and wakes the reconciler. Replacing this
// interface replaces the entire generation strategy without touching the
// controller's plumbing (ticker, lock/swap, publish).
type ReservationGenerator interface {
	// Generate snapshots the cluster and returns the desired failover host
	// reservations keyed by their object reference (name only; reservations are
	// cluster-scoped), together with the topology snapshot it reasoned over — the
	// controller needs topo.existing to wake the reconciler for every reservation
	// that may need pruning. ok=false means the pass should be abandoned without
	// touching the published desired state; the generator has already logged the
	// reason and recorded the abort metric.
	Generate(ctx context.Context) (desired map[corev1.ObjectReference]v1alpha1.Reservation, topo topology, ok bool)
}

// topology is the host layout a single calculation pass reasons over: every
// known hypervisor and the failover host reservations that exist right now. It
// is a consistent snapshot built once per pass by collect, so every pipeline
// stage reasons over the same view of the cluster.
type topology struct {
	// hypervisors indexes every known hypervisor by its name.
	hypervisors map[string]hv1.Hypervisor
	// existing holds the failover host reservations currently in the cluster,
	// keyed by their object reference. The planner prefers to keep these stable.
	existing map[corev1.ObjectReference]v1alpha1.Reservation
}

// pipelineGenerator is the production ReservationGenerator. It derives the
// desired reservations by running a four-stage pipeline, each stage a small
// interface so it can be understood, tested and swapped in isolation:
//
//  1. Seeder (seeding.go) enumerates the hypothetical placements to probe with:
//     every Nova flavor crossed with every availability zone in the fleet. These
//     flavor "seeds" replace the running vms the pipeline used to derive paths
//     from, so a partition is found even when its hosts run no vms and no single
//     exotic vm can veto a host from its partition.
//  2. PathFinder (pathfinding.go) probes the scheduler per seed and turns the
//     answers into the directed edges of a failover graph: source -> target means
//     "a host's whole workload can be evacuated onto target, and target is big
//     enough to hold it".
//  3. PartitionBuilder (partitioning.go) clusters those edges into partitions
//     (maximal sets of mutually-evacuable hosts).
//  4. ReservationPlanner (planning.go) decides, per partition, which hosts
//     become failover targets, honouring the configured ratio and minimum.
type pipelineGenerator struct {
	client.Client // k8s client

	// Monitor records the calculation-side Prometheus metrics (per-stage aborts
	// and the per-pass summary). It may be nil (e.g. in tests); every call site
	// nil-guards it.
	Monitor *Monitor

	// The pipeline stages run by Generate. They are defaulted to the production
	// implementations in SetupWithManager but can be set beforehand to substitute
	// fakes in tests.
	seeder             Seeder
	pathFinder         PathFinder
	partitionBuilder   PartitionBuilder
	reservationPlanner ReservationPlanner
}

// Generate runs one calculation pass of the failover host pipeline:
//
//  1. collect   — snapshot the cluster into a consistent topology.
//  2. Seeds     — enumerate the flavor × availability-zone permutations to probe.
//  3. FindPaths — probe the scheduler per seed and find, for each host, the hosts
//     its whole workload could be gang-evacuated onto (the directed edges of the
//     failover graph).
//  4. BuildPartitions — cluster those edges into failover partitions: maximal
//     sets of hosts that can all fail over onto one another. These cliques can
//     overlap, so Generate then assigns each shared host to exactly one partition
//     (the smallest) to keep the planner input disjoint.
//  5. PlanReservations — per partition, pick which hosts become failover
//     targets, honouring the configured ratio/minimum and keeping existing
//     reservations stable.
//
// Any error or inconsistency aborts the whole pass (ok=false) without producing
// a partial result: a partial recalculation is worse than a slightly stale one,
// as it could drop reservations that are still valid.
func (g *pipelineGenerator) Generate(ctx context.Context) (map[corev1.ObjectReference]v1alpha1.Reservation, topology, bool) {
	log := logf.FromContext(ctx).WithName("failoverhost-controller-calculation-loop")
	log.Info("recalculating failover host reservations")

	start := time.Now()

	topo, ok := g.collect(ctx)
	if !ok {
		g.Monitor.RecordCalculationAbort("collect")
		return nil, topology{}, false // collect already logged the reason.
	}

	// Enumerate the seeds (flavor × availability-zone permutations) to probe
	// with. A seeding failure (Nova unreachable, auth expired) is an
	// infrastructure error on a partial view, so abort the pass and keep the last
	// published picture rather than recomputing from a shrunken seed set.
	seeds, err := g.seeder.Seeds(ctx, topo)
	if err != nil {
		log.Error(err, "failed to generate failover seeds")
		g.Monitor.RecordCalculationAbort("seeding")
		return nil, topology{}, false
	}

	paths, err := g.pathFinder.FindPaths(ctx, topo, seeds)
	if err != nil {
		log.Error(err, "failed to find failover paths")
		g.Monitor.RecordCalculationAbort("pathfinding")
		return nil, topology{}, false
	}

	// Cluster the failover edges into partitions. See partitioning.go for why a
	// partition is a maximal clique of mutually-evacuable hosts, and how empty
	// hosts are recruited into the partition that can reach them.
	partitions := g.partitionBuilder.BuildPartitions(paths)
	log.V(1).Info("clustered failover paths into partitions",
		"paths", len(paths), "partitions", len(partitions))

	// Maximal cliques overlap: a host with a mutual edge into two otherwise
	// disjoint cliques (a—b, b—c but not a—c) appears in both. The planner keys
	// reservations by target host name ("failover-host-"+host), so a host picked
	// as a failover target by two partitions would yield two reservations with
	// the same name but conflicting SourceHosts, and whichever is written last to
	// the desired map wins — silently dropping the other partition's coverage. To
	// keep the planner input disjoint we assign each shared host to exactly one
	// partition: the smallest it belongs to. Assigning to the smallest keeps the
	// most partitions alive — a small partition only survives if it retains its
	// shared hosts, whereas a large one usually stays above the two-host minimum
	// even after giving one up — so failover space reaches as many partitions as
	// possible. Ties are broken by the partition's first host name so the choice
	// is deterministic. Partitions are already sorted by their first member, so a
	// stable sort by ascending size keeps that tiebreak order.
	sort.SliceStable(partitions, func(i, j int) bool {
		return len(partitions[i].Hosts) < len(partitions[j].Hosts)
	})
	assigned := map[string]bool{}
	deduped := make([]Partition, 0, len(partitions))
	for _, partition := range partitions {
		kept := make([]hv1.Hypervisor, 0, len(partition.Hosts))
		for _, hv := range partition.Hosts {
			if assigned[hv.Name] {
				continue
			}
			assigned[hv.Name] = true
			kept = append(kept, hv)
		}
		// Drop a partition emptied out by the dedupe, and one reduced below two
		// hosts: a lone host offers no failover peer (see PlanReservations).
		if len(kept) >= 2 {
			// Keep the partition's supported flavors as computed over its full
			// membership. Dropping a shared host can only relax the all-hosts
			// intersection, never tighten it, so every listed flavor still runs on
			// every remaining host — the set is at worst conservative.
			deduped = append(deduped, Partition{Hosts: kept, Flavors: partition.Flavors})
		}
	}
	// Restore the deterministic ordering the builder produced (by first host
	// name), which the size sort above disturbed.
	sort.SliceStable(deduped, func(i, j int) bool {
		return deduped[i].Hosts[0].Name < deduped[j].Hosts[0].Name
	})
	partitions = deduped
	labels := characterizePartitions(partitions)
	for i, partition := range partitions {
		// Describe what sets this partition apart from the others: the shared
		// fields no other partition shares, plus the flavors it can run. See
		// characterizePartitions and Partition.Flavors.
		log.V(1).Info("characterized failover partition",
			"partition", i, "partitionSize", len(partition.Hosts),
			"distinctBy", labels[i], "flavors", partition.Flavors)
	}

	// Plan the desired reservations per partition and collect them into one map.
	desired := make(map[corev1.ObjectReference]v1alpha1.Reservation)
	for i, partition := range partitions {
		description := describePartition(labels[i], partition.Flavors)
		planned := g.reservationPlanner.PlanReservations(topo, partition.Hosts, description)
		for _, reservation := range planned {
			desired[corev1.ObjectReference{Name: reservation.Name}] = reservation
		}
		log.V(1).Info("planned failover hosts for partition",
			"partitionSize", len(partition.Hosts), "planned", len(planned))
	}
	log.Info("generated desired failover host reservations",
		"partitions", len(partitions), "desiredReservations", len(desired),
		"existingReservations", len(topo.existing))
	g.Monitor.RecordCalculation(calculationSummary{
		duration:             time.Since(start),
		partitions:           len(partitions),
		desiredReservations:  len(desired),
		existingReservations: len(topo.existing),
	})

	return desired, topo, true
}

// collect snapshots the current cluster into a topology. It returns ok=false if
// the snapshot is inconsistent (e.g. a hypervisor lacks an availability zone),
// in which case the pass must abort rather than compute on a partial view.
func (g *pipelineGenerator) collect(ctx context.Context) (topology, bool) {
	log := logf.FromContext(ctx).WithName("failoverhost-controller-calculation-loop")

	hvs := &hv1.HypervisorList{}
	if err := g.List(ctx, hvs); err != nil {
		log.Error(err, "failed to list hypervisors")
		return topology{}, false
	}
	hvsByName := make(map[string]hv1.Hypervisor)
	for i := range hvs.Items {
		hvsByName[hvs.Items[i].Name] = hvs.Items[i]
	}
	log.V(1).Info("listed cluster state", "hypervisors", len(hvsByName))

	// Every hypervisor must carry an availability zone: the seeder permutes
	// flavors across these AZs, and the planner stamps each reservation with its
	// host's AZ. A hypervisor missing its AZ label is a torn snapshot, so abort
	// rather than seed or plan from an inconsistent set of hosts.
	for name, hv := range hvsByName {
		if _, ok := hv.Labels[corev1.LabelTopologyZone]; !ok {
			log.Error(nil, "hypervisor has no availability zone, aborting", "host", name)
			return topology{}, false
		}
	}

	allReservations := &v1alpha1.ReservationList{}
	if err := g.List(ctx, allReservations); err != nil {
		log.Error(err, "failed to list existing failover host reservations")
		return topology{}, false
	}
	existing := make(map[corev1.ObjectReference]v1alpha1.Reservation)
	for _, reservation := range allReservations.Items {
		if reservation.Spec.Type == v1alpha1.ReservationTypeFailoverHost {
			existing[corev1.ObjectReference{Name: reservation.Name}] = reservation
		}
	}

	return topology{hypervisors: hvsByName, existing: existing}, true
}
