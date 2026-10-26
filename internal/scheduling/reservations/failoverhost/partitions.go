// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package failoverhost

import (
	"encoding/json"
	"fmt"
	"maps"
	"reflect"
	"slices"
	"sort"
	"strings"

	hv1 "github.com/cobaltcore-dev/openstack-hypervisor-operator/api/v1"
	"k8s.io/apimachinery/pkg/api/resource"
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

	// effectiveCapacity returns a host's gang-fit capacity: its base capacity with
	// effectiveCapacity overlaid on top (fallback = capacity), matching how the
	// PathFinder sizes hosts. Copied into a fresh map so we never mutate Status.
	effectiveCapacity := func(name string) map[hv1.ResourceName]resource.Quantity {
		capa := map[hv1.ResourceName]resource.Quantity{}
		maps.Copy(capa, hvsByName[name].Status.Capacity)
		maps.Copy(capa, hvsByName[name].Status.EffectiveCapacity)
		return capa
	}
	for _, empty := range emptyHosts {
		emptyCapa := effectiveCapacity(empty)
		recruited := false
		for i, clique := range cliques {
			allReach := len(clique) > 0
			for _, member := range clique {
				// Every member must reach the empty host (member can evacuate onto
				// it), and the empty host must be the same effective size as the
				// member. The empty host has no vms, so Step 1 never produced an
				// empty->member edge to prove it can rescue its peers; we fall back
				// to the same gang-fit rule and require equal capacity. A larger
				// empty host would be recorded as a source of a smaller reserved
				// target it could never actually evacuate onto.
				if !outgoing[member][empty] || cmpResources(emptyCapa, effectiveCapacity(member)) != 0 {
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

// characterizePartitions returns a short, human-readable label per partition
// describing what sets it apart from the others, e.g. "spec.aggregates=[hana]".
// The result is index-aligned with partitions.
//
// For each partition we first intersect its hypervisors down to the leaf paths
// whose value is identical across all of them — the fields that implicitly make
// those hosts a partition (a common AZ, aggregate, or hypervisor type), without
// hard-coding which fields matter. We then keep only the *distinguishing* leaves:
// a field is dropped if its shared value is identical across every partition, as
// such fleet-wide constants (default booleans, versions, ...) say nothing about
// what makes a partition special. The surviving leaves are rendered as a compact,
// sorted "key=value key=value" string for at-a-glance logging.
func characterizePartitions(partitions [][]hv1.Hypervisor) []string {
	// flatten serializes a hypervisor to its leaf paths, keyed by dotted path.
	flatten := func(hv hv1.Hypervisor) map[string]any {
		raw, err := json.Marshal(hv)
		if err != nil {
			return nil
		}
		var nested map[string]any
		if err := json.Unmarshal(raw, &nested); err != nil {
			return nil
		}
		flat := make(map[string]any)
		var walk func(prefix string, v any)
		walk = func(prefix string, v any) {
			if m, ok := v.(map[string]any); ok {
				for k, vv := range m {
					key := k
					if prefix != "" {
						key = prefix + "." + k
					}
					walk(key, vv)
				}
				return
			}
			flat[prefix] = v
		}
		walk("", nested)
		return flat
	}
	// shared[i] holds the leaves common to every hypervisor in partition i.
	shared := make([]map[string]any, len(partitions))
	for i, partition := range partitions {
		if len(partition) == 0 {
			shared[i] = map[string]any{}
			continue
		}
		common := flatten(partition[0])
		for _, hv := range partition[1:] {
			flat := flatten(hv)
			for key, want := range common {
				if got, ok := flat[key]; !ok || !reflect.DeepEqual(got, want) {
					delete(common, key)
				}
			}
		}
		shared[i] = common
	}
	// Count how many partitions carry each (key, value). A field common to all
	// of them is a fleet-wide constant and tells us nothing distinguishing.
	seen := map[string]int{}
	for _, common := range shared {
		for key, val := range common {
			seen[key+"="+fmt.Sprintf("%v", val)]++
		}
	}
	// Render the distinguishing leaves of each partition as a sorted label. A
	// field is distinguishing unless every partition shares it (a fleet-wide
	// constant); with a single partition there is nothing to contrast against,
	// so all its shared fields survive.
	labels := make([]string, len(partitions))
	for i, common := range shared {
		parts := make([]string, 0, len(common))
		for key, val := range common {
			pair := key + "=" + fmt.Sprintf("%v", val)
			if len(partitions) == 1 || seen[pair] < len(partitions) {
				parts = append(parts, pair)
			}
		}
		slices.Sort(parts)
		labels[i] = strings.Join(parts, " ")
	}
	return labels
}
