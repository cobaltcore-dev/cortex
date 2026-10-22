// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package failoverhost

import (
	"maps"
	"slices"
	"sort"

	hv1 "github.com/cobaltcore-dev/openstack-hypervisor-operator/api/v1"
)

// PartitionBuilder is the second pipeline stage. It clusters the directed
// failover edges found by the PathFinder into failover partitions.
//
// A failover partition is a maximal set of hypervisors in which every host can
// be evacuated onto every other host. For example, HANA hosts, general-purpose
// hosts and external-customer hosts typically each form their own partition,
// because their workloads can only ever fail over within the group.
type PartitionBuilder interface {
	// BuildPartitions groups the [source, target] failover edges into
	// partitions. The output is deterministic regardless of edge order.
	BuildPartitions(paths [][2]hv1.Hypervisor) [][]hv1.Hypervisor
}

// cliquePartitionBuilder builds partitions by finding maximal cliques in the
// undirected graph of mutual failover edges: an undirected edge a—b exists only
// if both a→b and b→a are valid paths, since failover requires that the hosts
// can evacuate onto each other. A failover partition is exactly a maximal clique
// in that graph — a largest-possible set of hosts all pairwise mutually
// evacuable.
//
// Empty hosts (hosts without any outgoing edge, e.g. because they host no vms)
// fall out as single-element cliques. They are recruited into an existing
// partition whose members all have an outgoing edge toward the empty host, as
// we then assume the reverse direction is also possible.
type cliquePartitionBuilder struct{}

// BuildPartitions implements PartitionBuilder. It works in four steps:
//
//  1. Build the directed adjacency from the edges, and note which hosts have no
//     outgoing edge at all (the "empty" hosts, handled specially in step 3).
//  2. Derive the undirected "mutual" adjacency (a—b iff both a→b and b→a), and
//     run Bron-Kerbosch over it to enumerate every maximal clique. Each clique
//     is one partition. Bron-Kerbosch recursively grows a candidate clique R by
//     trying each still-eligible vertex in P, intersecting P and the
//     already-seen set X with that vertex's neighbours; a clique is maximal when
//     both P and X are empty. Non-empty hosts with no mutual edge are emitted as
//     their own single-host partitions.
//  3. Recruit each empty host into the first partition all of whose members can
//     reach it (member→empty for every member). An empty host that no partition
//     fully reaches stays on its own.
//  4. Translate host names back to Hypervisor values and sort, so the result is
//     deterministic despite the map iteration used throughout.
func (cliquePartitionBuilder) BuildPartitions(paths [][2]hv1.Hypervisor) [][]hv1.Hypervisor {
	// Collect all hosts and the directed adjacency, keyed by host name.
	hvsByName := map[string]hv1.Hypervisor{}
	outgoing := map[string]map[string]bool{}
	for _, path := range paths {
		src, dst := path[0], path[1]
		hvsByName[src.Name] = src
		hvsByName[dst.Name] = dst
		if outgoing[src.Name] == nil {
			outgoing[src.Name] = map[string]bool{}
		}
		outgoing[src.Name][dst.Name] = true
	}

	// Build the undirected adjacency from mutual edges only. Hosts without any
	// outgoing edge are empty hosts and are recruited into partitions later.
	emptyHosts := []string{}
	for name := range hvsByName {
		if len(outgoing[name]) == 0 {
			emptyHosts = append(emptyHosts, name)
		}
	}
	mutual := map[string]map[string]bool{}
	for a, dsts := range outgoing {
		for b := range dsts {
			if outgoing[b][a] { // b also reaches a, so the edge is mutual
				if mutual[a] == nil {
					mutual[a] = map[string]bool{}
				}
				mutual[a][b] = true
			}
		}
	}

	// Find all maximal cliques in the undirected mutual graph using the
	// Bron-Kerbosch algorithm. These are our failover partitions.
	cliques := [][]string{}
	all := map[string]bool{}
	for a := range mutual {
		all[a] = true
	}
	var bronKerbosch func(r, p, x map[string]bool)
	bronKerbosch = func(r, p, x map[string]bool) {
		if len(p) == 0 && len(x) == 0 {
			if len(r) == 0 {
				return // Don't emit the empty clique when there are no mutual edges.
			}
			clique := make([]string, 0, len(r))
			for name := range r {
				clique = append(clique, name)
			}
			cliques = append(cliques, clique)
			return
		}
		// Iterate over a copy of p, as p is mutated inside the loop.
		candidates := make([]string, 0, len(p))
		for v := range p {
			candidates = append(candidates, v)
		}
		for _, v := range candidates {
			if !p[v] {
				continue
			}
			nr := map[string]bool{v: true}
			maps.Copy(nr, r)
			np, nx := map[string]bool{}, map[string]bool{}
			for n := range p {
				if mutual[v][n] {
					np[n] = true
				}
			}
			for n := range x {
				if mutual[v][n] {
					nx[n] = true
				}
			}
			bronKerbosch(nr, np, nx)
			delete(p, v)
			x[v] = true
		}
	}
	bronKerbosch(map[string]bool{}, all, map[string]bool{})

	// Non-empty hosts that share no mutual edge with any other host are not part
	// of any clique. They form their own single-element partition.
	covered := map[string]bool{}
	for _, clique := range cliques {
		for _, name := range clique {
			covered[name] = true
		}
	}
	for name := range outgoing {
		if !covered[name] {
			cliques = append(cliques, []string{name})
		}
	}

	// Sort before recruitment so the result is deterministic: Bron-Kerbosch and
	// the loops above both iterate maps, so clique contents and order are random
	// per run. We sort each clique's members, then order the cliques, so an empty
	// host reachable by several cliques is always recruited into the same one.
	for _, clique := range cliques {
		slices.Sort(clique)
	}
	sort.SliceStable(cliques, func(i, j int) bool {
		return slices.Compare(cliques[i], cliques[j]) < 0
	})
	slices.Sort(emptyHosts)

	for _, empty := range emptyHosts {
		recruited := false
		for i, clique := range cliques {
			allReach := len(clique) > 0
			for _, member := range clique {
				if !outgoing[member][empty] {
					allReach = false
					break
				}
			}
			if allReach {
				cliques[i] = append(clique, empty)
				recruited = true
				break
			}
		}
		if !recruited {
			cliques = append(cliques, []string{empty})
		}
	}

	// Translate the clique host names back into hypervisors. Sort for
	// deterministic output, as the clustering relies on map iteration.
	partitions := make([][]hv1.Hypervisor, 0, len(cliques))
	for _, clique := range cliques {
		slices.Sort(clique)
		partition := make([]hv1.Hypervisor, 0, len(clique))
		for _, name := range clique {
			partition = append(partition, hvsByName[name])
		}
		partitions = append(partitions, partition)
	}
	sort.SliceStable(partitions, func(i, j int) bool {
		return partitions[i][0].Name < partitions[j][0].Name
	})
	return partitions
}
