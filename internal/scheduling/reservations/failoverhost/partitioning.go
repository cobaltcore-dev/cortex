// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package failoverhost

import (
	"fmt"
	"maps"
	"slices"
	"sort"
	"strconv"
	"strings"

	hv1 "github.com/cobaltcore-dev/openstack-hypervisor-operator/api/v1"
	"k8s.io/apimachinery/pkg/api/resource"
)

// Partition is one failover partition: a set of hypervisors in which every host
// can be evacuated onto every other host, plus the flavors that whole set can
// run. Flavors answers "which flavors can be spawned anywhere in this partition":
// a flavor is listed only if *every* host in the partition accepts it, so a
// listed flavor is guaranteed to run on all of them. Flavors is sorted and may be
// empty — e.g. when the members' accept sets are disjoint, so no single flavor is
// proven to fit everywhere in the partition.
type Partition struct {
	// Hosts are the partition's members, sorted by name.
	Hosts []hv1.Hypervisor
	// Flavors are the flavor names accepted on every host of the partition,
	// sorted.
	Flavors []string
}

// PartitionBuilder is the third pipeline stage. It clusters the directed
// failover edges found by the PathFinder into failover partitions.
//
// A failover partition is a maximal set of hypervisors in which every host can
// be evacuated onto every other host. For example, HANA hosts, general-purpose
// hosts and external-customer hosts typically each form their own partition,
// because their workloads can only ever fail over within the group.
type PartitionBuilder interface {
	// BuildPartitions groups the failover Paths into partitions, each carrying the
	// flavors its whole host set can run. The output is deterministic regardless
	// of edge order.
	BuildPartitions(paths []Path) []Partition
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

// BuildPartitions implements PartitionBuilder. It works in five steps:
//
//  1. Build the directed adjacency from the edges, record which flavors each host
//     accepts (it is an endpoint of an edge carrying that flavor), and note which
//     hosts have no outgoing edge at all (the "empty" hosts, handled specially in
//     step 3).
//  2. Derive the undirected "mutual" adjacency (a—b iff both a→b and b→a *and*
//     a and b accept the same flavors), and run Bron-Kerbosch over it to
//     enumerate every maximal clique. Each clique is one partition. The
//     same-flavors requirement enforces the ADR 0011 whole-host guarantee: any
//     clique member may become the failover target with the rest its sources, so
//     the target must run every flavor any source accepts; a single co-accepted
//     flavor is enough to forge a mutual edge but not to prove this. Bron-Kerbosch
//     recursively grows a candidate clique R by trying each still-eligible vertex
//     in P, intersecting P and the already-seen set X with that vertex's
//     neighbours; a clique is maximal when both P and X are empty. Non-empty hosts
//     with no mutual edge are emitted as their own single-host partitions.
//  3. Recruit each empty host into the first partition all of whose members can
//     reach it (member→empty for every member). An empty host that no partition
//     fully reaches stays on its own.
//  4. Translate host names back to Hypervisor values and sort, so the result is
//     deterministic despite the map iteration used throughout.
//  5. Compute each partition's supported flavors: the flavors accepted on *every*
//     one of its hosts (the intersection of the per-host accept sets from step 1).
func (cliquePartitionBuilder) BuildPartitions(paths []Path) []Partition {
	// Collect all hosts and the directed adjacency, keyed by host name. Also record
	// which flavors each host accepts: a host is an endpoint of an edge exactly
	// when it co-accepted that edge's flavors, so both endpoints accept them.
	hvsByName := map[string]hv1.Hypervisor{}
	outgoing := map[string]map[string]bool{}
	acceptedBy := map[string]map[string]bool{}
	accept := func(host string, flavors []string) {
		if acceptedBy[host] == nil {
			acceptedBy[host] = map[string]bool{}
		}
		for _, flavor := range flavors {
			acceptedBy[host][flavor] = true
		}
	}
	for _, path := range paths {
		src, dst := path.Source, path.Target
		hvsByName[src.Name] = src
		hvsByName[dst.Name] = dst
		if outgoing[src.Name] == nil {
			outgoing[src.Name] = map[string]bool{}
		}
		outgoing[src.Name][dst.Name] = true
		accept(src.Name, path.Flavors)
		accept(dst.Name, path.Flavors)
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
			// b must also reach a (the edge is mutual), and the two must accept the
			// same flavors. The clique both hosts land in may designate either of
			// them the failover target, with the other becoming its source; a target
			// must run every flavor its source accepts (the ADR 0011 whole-host
			// guarantee), so hosts with differing accept sets must never be merged.
			// A single co-accepted flavor is enough to forge a mutual edge, but it is
			// not enough to prove whole-host evacuability — e.g. two hosts sharing a
			// general flavor where only one also runs a trait-gated flavor.
			if outgoing[b][a] && sameFlavors(acceptedBy[a], acceptedBy[b]) {
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
				// it), the empty host must be the same effective size as the member,
				// and the two must accept the same flavors. The empty host has no vms,
				// so Step 1 never produced an empty->member edge to prove it can rescue
				// its peers; we fall back to the same gang-fit rule and require equal
				// capacity. A larger empty host would be recorded as a source of a
				// smaller reserved target it could never actually evacuate onto. The
				// flavor check mirrors the mutual-edge rule: a recruited host may become
				// the partition's target, so it must run every flavor its peers accept.
				if !outgoing[member][empty] ||
					cmpResources(emptyCapa, effectiveCapacity(member)) != 0 ||
					!sameFlavors(acceptedBy[empty], acceptedBy[member]) {
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

	// Translate the clique host names back into hypervisors and compute each
	// partition's supported flavors: those accepted on *every* host, so a listed
	// flavor is guaranteed to run anywhere in the partition. Sort for deterministic
	// output, as the clustering relies on map iteration.
	partitions := make([]Partition, 0, len(cliques))
	for _, clique := range cliques {
		slices.Sort(clique)
		hosts := make([]hv1.Hypervisor, 0, len(clique))
		for _, name := range clique {
			hosts = append(hosts, hvsByName[name])
		}
		partitions = append(partitions, Partition{
			Hosts:   hosts,
			Flavors: sharedFlavors(clique, acceptedBy),
		})
	}
	sort.SliceStable(partitions, func(i, j int) bool {
		return partitions[i].Hosts[0].Name < partitions[j].Hosts[0].Name
	})
	return partitions
}

// sameFlavors reports whether two hosts accept exactly the same set of flavors.
// It is the flavor-compatibility test for merging hosts into a partition: since
// any member can become the partition's failover target and the rest its
// sources, the target must run every flavor any source accepts, so all members
// must share one accept set. An empty host's accept set comes from its inbound
// edges (see acceptedBy), so a recruited host is compared the same way.
func sameFlavors(a, b map[string]bool) bool {
	if len(a) != len(b) {
		return false
	}
	for flavor := range a {
		if !b[flavor] {
			return false
		}
	}
	return true
}

// sharedFlavors returns the flavors accepted on *every* host in the clique,
// sorted. A host accepts a flavor when it is an endpoint of an edge the path
// finder tagged with that flavor (both endpoints co-accepted it), so even a
// recruited host counts through its inbound edges. The result is empty when the
// members' accept sets don't intersect.
func sharedFlavors(clique []string, acceptedBy map[string]map[string]bool) []string {
	if len(clique) == 0 {
		return nil
	}
	shared := []string{}
	for flavor := range acceptedBy[clique[0]] {
		onAll := true
		for _, host := range clique[1:] {
			if !acceptedBy[host][flavor] {
				onAll = false
				break
			}
		}
		if onAll {
			shared = append(shared, flavor)
		}
	}
	slices.Sort(shared)
	return shared
}

// characterizePartitions returns a short, human-readable label per partition
// describing what sets it apart from the others, e.g. "spec.aggregates=[hana]".
// The result is index-aligned with partitions.
//
// It compares partitions on just the three dimensions that actually shape a
// failover partition — aggregates, traits and (rounded) capacity — rather than
// reflecting over every field of the hypervisor. For each dimension we take the
// value shared by all of a partition's hosts (dropping it if they disagree), then
// keep only the *distinguishing* ones: a dimension is dropped if its shared value
// is identical across every partition, as such a fleet-wide constant says nothing
// about what makes a partition special. The survivors are rendered as a compact,
// sorted "key=value key=value" string for at-a-glance logging. Flavor support is
// reported separately (see Partition.Flavors), so it is not repeated here.
func characterizePartitions(partitions []Partition) []string {
	// fields returns the three comparable dimensions of a hypervisor, each as a
	// stable string: its aggregates, its traits, and its capacity rounded to GiB
	// (so tiny per-node memory discrepancies don't split an otherwise-shared
	// value). Capacity reuses hvCapacity and the GiB rounding cmpResources applies.
	fields := func(hv hv1.Hypervisor) map[string]string {
		aggregates := slices.Clone(hv.Spec.Aggregates)
		slices.Sort(aggregates)
		traits := slices.Clone(hv.Status.Traits)
		slices.Sort(traits)
		capa := hvCapacity(hv)
		const gib = 1 << 30
		memGi := capa[hv1.ResourceMemory]
		cpu := capa[hv1.ResourceCPU]
		return map[string]string{
			"spec.aggregates":       fmt.Sprintf("%v", aggregates),
			"status.traits":         fmt.Sprintf("%v", traits),
			"status.capacity.memGi": strconv.FormatInt(memGi.Value()/gib, 10),
			"status.capacity.cpu":   cpu.String(),
		}
	}
	// shared[i] holds the dimensions common to every hypervisor in partition i.
	shared := make([]map[string]string, len(partitions))
	for i, partition := range partitions {
		if len(partition.Hosts) == 0 {
			shared[i] = map[string]string{}
			continue
		}
		common := fields(partition.Hosts[0])
		for _, hv := range partition.Hosts[1:] {
			f := fields(hv)
			for key, want := range common {
				if f[key] != want {
					delete(common, key)
				}
			}
		}
		shared[i] = common
	}
	// Count how many partitions carry each (key, value). A dimension common to all
	// of them is a fleet-wide constant and tells us nothing distinguishing.
	seen := map[string]int{}
	for _, common := range shared {
		for key, val := range common {
			seen[key+"="+val]++
		}
	}
	// Render the distinguishing dimensions of each partition as a sorted label. A
	// dimension is distinguishing unless every partition shares it (a fleet-wide
	// constant); with a single partition there is nothing to contrast against, so
	// all its shared dimensions survive.
	labels := make([]string, len(partitions))
	for i, common := range shared {
		parts := make([]string, 0, len(common))
		for key, val := range common {
			pair := key + "=" + val
			if len(partitions) == 1 || seen[pair] < len(partitions) {
				parts = append(parts, pair)
			}
		}
		slices.Sort(parts)
		labels[i] = strings.Join(parts, " ")
	}
	return labels
}

// maxDescribedFlavors caps how many flavor names describePartition spells out, so
// a partition that runs hundreds of flavors doesn't bloat the reservation's
// PartitionDescription field. The rest are summarized as a "(+k more)" count; the
// full set is still logged per pass.
const maxDescribedFlavors = 5

// describePartition renders the human-readable PartitionDescription stored on each
// reservation: the distinguishing label (see characterizePartitions) followed by
// the flavors the partition can run (see Partition.Flavors), e.g.
// "spec.aggregates=[hana] | flavors: hana.4xl, hana.8xl (+3 more)". flavors is
// assumed sorted. Either half is omitted when empty, so a partition with no
// distinguishing label still reports its flavors and vice versa.
func describePartition(label string, flavors []string) string {
	parts := []string{}
	if label != "" {
		parts = append(parts, label)
	}
	if len(flavors) > 0 {
		shown := flavors
		suffix := ""
		if len(flavors) > maxDescribedFlavors {
			shown = flavors[:maxDescribedFlavors]
			suffix = fmt.Sprintf(" (+%d more)", len(flavors)-maxDescribedFlavors)
		}
		parts = append(parts, "flavors: "+strings.Join(shown, ", ")+suffix)
	}
	return strings.Join(parts, " | ")
}
