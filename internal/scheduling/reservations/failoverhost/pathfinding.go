// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package failoverhost

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"maps"
	"net/http"
	"slices"
	"sort"
	"strconv"
	"time"

	api "github.com/cobaltcore-dev/cortex/api/external/nova"
	"github.com/cobaltcore-dev/cortex/api/scheduling"
	"github.com/cobaltcore-dev/cortex/api/v1alpha1"
	hv1 "github.com/cobaltcore-dev/openstack-hypervisor-operator/api/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
)

// Path is one directed edge of the failover graph: source's whole workload can
// be gang-evacuated onto target. It also carries the flavors whose seed proved
// the edge — i.e. the flavors source and target co-accept — so downstream stages
// can report which flavors a partition can actually run. Flavors is sorted and
// deduplicated.
type Path struct {
	// Source and Target are the endpoints: source can be evacuated onto target.
	Source, Target hv1.Hypervisor
	// Flavors are the names of the flavor seeds both endpoints co-accepted, which
	// is what produced this edge. Sorted and deduplicated.
	Flavors []string
}

// PathFinder is the second pipeline stage. It discovers the directed edges of the
// failover graph: for every host it reports the hosts that host's workload could
// be gang-evacuated onto. An edge source->target means "a host's whole workload
// can be evacuated onto target, and target is large enough to hold it".
//
// Edges are no longer derived from the vms that happen to run on a host. Instead
// we probe with flavor seeds (see Seeder): for every (flavor, availability zone)
// permutation we ask the scheduler which hosts would accept it, and treat the
// hosts that co-accept a seed as mutually compatible under that flavor. This
// escapes two blind spots of vm-seeding — partitions whose hosts run no vms, and
// a single exotic vm vetoing a host from its partition — both documented on Seed.
//
// We probe with evacuation intent: a failover ultimately cold-migrates
// (evacuates) a dead host's workload, and evacuation is the weaker check — it
// does not verify the CPU/feature compatibility that live migration does. We
// deliberately do not probe live-migratability here even though a freshly
// reserved host is drained by live migration before its reservation goes active:
// we assume a partition holds enough peers to absorb that drain, which later
// draining code arranges, rather than proving it per host at partition time.
//
// The direction matters. source->target does not imply target->source: target
// might be too small for source's workload even if the reverse fits. The
// PartitionBuilder later keeps only the mutual edges, so this stage is free to
// report one-directional possibilities.
type PathFinder interface {
	// FindPaths returns the failover edges as Paths, discovered by probing the
	// scheduler with the given seeds; each Path also records the flavors its
	// endpoints co-accepted. A seed the scheduler accepts on fewer than two hosts
	// yields no edge and is not an error. Any lookup error, by contrast, is an
	// infrastructure failure (scheduler unreachable/timed out/non-200) that
	// invalidates the pass, so it is returned to abort it rather than silently
	// collapsing the failover graph.
	FindPaths(ctx context.Context, topo topology, seeds []Seed) ([]Path, error)
}

// evacuationPathFinder finds failover paths by asking the external cortex
// scheduler, per seed, which hosts would accept that flavor, then pairing up the
// co-accepting hosts subject to gang-level fit.
//
// Asking the real scheduler (rather than reimplementing placement here) is
// deliberate: it means failover paths are computed with exactly the same
// filters, weighers and constraints that govern ordinary placement, so a
// partition can never contain a target the scheduler would actually reject.
type evacuationPathFinder struct {
	// httpClient issues the scheduler requests. Give it a timeout so a hung
	// scheduler cannot stall the whole calculation loop. Separately injected so
	// tests can point it at a stub server.
	httpClient *http.Client
	// schedulerURL is the external cortex scheduler endpoint to POST to.
	schedulerURL string
	// monitor records scheduler request metrics. May be nil (e.g. in tests).
	monitor *Monitor
}

// FindPaths implements PathFinder.
//
// For each seed we ask the scheduler which hosts would accept that flavor in
// that availability zone. Every pair of hosts that co-accept a seed is mutually
// compatible under that flavor, so for each such pair (a, b) we emit the directed
// edge a->b whenever b is at least as large as a. The direction encodes the hard,
// gang-level fit constraint: a drain/evacuation moves a whole host's workload
// onto the target at once, so the target must be at least as large as the source.
// Equal-sized co-accepting hosts therefore yield edges both ways, which the
// PartitionBuilder turns into a mutual edge and hence a clique; an undersized
// target only ever appears as a source, never a target, so it is never chosen as
// a landing zone for a larger peer.
//
// Edges are deduplicated: the same pair can be co-accepted by many seeds (e.g.
// every flavor a pair of identical hosts both run), but the graph only needs each
// directed edge once. Capacity is compared on fresh copies of the hypervisors'
// capacity maps so we never mutate the shared Status.Capacity.
func (f *evacuationPathFinder) FindPaths(ctx context.Context, topo topology, seeds []Seed) ([]Path, error) {
	log := logf.FromContext(ctx)

	// edges[src][dst] records the flavor seeds that produced a discovered directed
	// edge, deduplicating across seeds. We materialize it into the []Path slice at
	// the end, carrying each edge's supporting flavors.
	edges := map[string]map[string]map[string]bool{}
	for _, seed := range seeds {
		accepting, err := f.fetchSeedCandidates(ctx, seed, topo.hypervisors)
		if err != nil {
			// A lookup error is an infrastructure failure (the scheduler is
			// unreachable, timed out, or returned a non-200) — not an answer that
			// this seed has no candidates, which comes back as a short slice with a
			// nil error. We cannot assert anything about the failover graph on a
			// partial view, so abort the whole pass. The caller leaves the desired
			// state untouched and retries next cycle; swallowing the error here
			// would instead collapse every host's paths and prune all still-valid
			// reservations.
			return nil, fmt.Errorf(
				"failed to fetch candidates for flavor %q in az %q: %w",
				seed.Flavor.Name, seed.AvailabilityZone, err)
		}
		// Fewer than two accepting hosts can form no pair, so contribute no edge.
		if len(accepting) < 2 {
			continue
		}
		acceptingNames := make([]string, len(accepting))
		for i, hv := range accepting {
			acceptingNames[i] = hv.Name
		}
		log.V(1).Info("fetched candidates for seed",
			"flavor", seed.Flavor.Name, "az", seed.AvailabilityZone, "hosts", acceptingNames)
		// Pair up every co-accepting host, emitting source->target when the target
		// is at least as large as the source (the gang-fit constraint). Record the
		// seed's flavor on the edge so downstream stages know which flavors the pair
		// co-accepts.
		for _, src := range accepting {
			for _, dst := range accepting {
				if src.Name == dst.Name {
					continue
				}
				if cmpResources(hvCapacity(src), hvCapacity(dst)) > 0 {
					continue // Target smaller than source: it cannot hold the gang.
				}
				if edges[src.Name] == nil {
					edges[src.Name] = map[string]map[string]bool{}
				}
				if edges[src.Name][dst.Name] == nil {
					edges[src.Name][dst.Name] = map[string]bool{}
				}
				edges[src.Name][dst.Name][seed.Flavor.Name] = true
			}
		}
	}

	// Materialize the deduplicated edges into Paths, sorted so the output is
	// deterministic despite the map iteration above. Each edge's flavor set is
	// flattened into a sorted slice.
	paths := []Path{}
	srcNames := make([]string, 0, len(edges))
	for src := range edges {
		srcNames = append(srcNames, src)
	}
	sort.Strings(srcNames)
	for _, src := range srcNames {
		dstNames := make([]string, 0, len(edges[src]))
		for dst := range edges[src] {
			dstNames = append(dstNames, dst)
		}
		sort.Strings(dstNames)
		for _, dst := range dstNames {
			flavors := make([]string, 0, len(edges[src][dst]))
			for flavor := range edges[src][dst] {
				flavors = append(flavors, flavor)
			}
			sort.Strings(flavors)
			paths = append(paths, Path{
				Source:  topo.hypervisors[src],
				Target:  topo.hypervisors[dst],
				Flavors: flavors,
			})
			log.V(1).Info("added failover path",
				"sourceHost", src, "targetHost", dst, "flavors", flavors)
		}
	}
	return paths, nil
}

// hvCapacity returns a hypervisor's gang-fit capacity: its base capacity with
// effectiveCapacity overlaid on top. Copied into a fresh map so we never mutate
// the shared Status.
func hvCapacity(hv hv1.Hypervisor) map[hv1.ResourceName]resource.Quantity {
	capa := map[hv1.ResourceName]resource.Quantity{}
	maps.Copy(capa, hv.Status.Capacity)
	maps.Copy(capa, hv.Status.EffectiveCapacity)
	return capa
}

// fetchSeedCandidates asks the external cortex scheduler which of the known
// hypervisors would accept the seed's flavor in its availability zone, and
// returns that subset.
//
// The request is shaped to mirror a real evacuation as closely as possible: it
// carries the seed's availability zone, flavor and a KVM image hypervisor type so
// the same filters apply, and offers every known host as a candidate (Hosts) so
// the scheduler has a host set to filter over. Four options matter:
//
//   - ReadOnly, so probing placements never mutate shared scheduling state.
//   - SkipInflight and SkipCommittedResourceTracking, which ReadOnly runs must
//     set (scheduling.Options.Validate rejects a ReadOnly run otherwise, and the
//     scheduler calls Validate) so this probe neither creates in-flight
//     reservations nor writes CR allocations.
//   - IgnoredReservationTypes = FailoverHost, so existing failover host
//     reservations don't block the very hosts we are reasoning about — otherwise
//     a reserved host would be hidden from its own partition and the clustering
//     would fragment.
//   - AssumeEmptyHosts, so candidates are judged by their total capacity rather
//     than their current free capacity. A failover target is reserved *empty*, so
//     what matters for partitioning is whether a host could hold the workload once
//     cleared, not whether it has room right now. Without this a busy host never
//     accepts anyone, gets no inbound links, forms no mutual edge, and stays
//     orphaned — even though reserving it would drain it. Compatibility (flavors,
//     traits, AZ) is thus occupancy-independent; which host is actually cheapest
//     to empty is left to the planner's candidate ordering. The hard whole-host
//     fit check in FindPaths still compares *total* capacities, so an undersized
//     target is dropped regardless of this option.
//
// We probe with evacuation intent (the "evacuate" _nova_check_type, which maps to
// EvacuateIntent in GetIntent) and set no source_host: a failover evacuates (cold
// migrates) the workload, and evacuation does not run the live-migratability
// feature check, so there is no source host to anchor it to. This is weaker than
// the old live-migration probe, which is intentional — see the PathFinder doc.
func (f *evacuationPathFinder) fetchSeedCandidates(ctx context.Context, seed Seed, hvsByName map[string]hv1.Hypervisor) ([]hv1.Hypervisor, error) {
	log := logf.FromContext(ctx)

	// Build valid image properties. We set the image hypervisor type explicitly so
	// the filter that checks flavor/image compatibility can evaluate it. Since we
	// only handle KVM virtual machines, we set it to KVM.
	imageProps := map[string]any{
		"img_hv_type": string(api.NovaImageMetaHVTypeKVM),
	}
	// Offer every known host as a candidate. The scheduler filters/weighs over
	// exactly this host set — it does not gather the cluster itself — so leaving
	// Hosts empty would make it return no candidates. Each host also needs a
	// weight entry, which the scheduler requires to accompany the host set.
	hosts := make([]api.ExternalSchedulerHost, 0, len(hvsByName))
	weights := make(map[string]float64, len(hvsByName))
	for name := range hvsByName {
		hosts = append(hosts, api.ExternalSchedulerHost{ComputeHost: name})
		weights[name] = 0
	}
	requestObj := api.ExternalSchedulerRequest{
		Hosts:   hosts,
		Weights: weights,
		Options: scheduling.Options{
			// Read-only probe: don't write any resources. ReadOnly additionally
			// requires SkipInflight and SkipCommittedResourceTracking, else the
			// scheduler rejects the request in Options.Validate.
			ReadOnly:                      true,
			SkipInflight:                  true,
			SkipCommittedResourceTracking: true,
			// Judge candidates by total capacity, not current free capacity: a
			// failover target is reserved empty, so a busy host that would fit the
			// workload once cleared must still be a valid candidate. Without this,
			// occupied hosts never accept anyone and stay orphaned.
			AssumeEmptyHosts: true,
			// Ignore existing failover host reservations, otherwise they would
			// block these hosts, separate them from their failover partition and
			// break the clustering.
			IgnoredReservationTypes: []v1alpha1.ReservationType{
				v1alpha1.ReservationTypeFailoverHost,
			},
		},
		Spec: api.NovaObject[api.NovaSpec]{
			Data: api.NovaSpec{
				NumInstances:     1,
				AvailabilityZone: seed.AvailabilityZone,
				// Probe with evacuation intent. A failover cold-migrates (evacuates)
				// the workload, and evacuation does not run the live-migratability
				// feature check, so no source_host is needed. The check type must be
				// the literal "evacuate" nova uses, which maps to EvacuateIntent in
				// GetIntent. See the PathFinder doc for why this is deliberately
				// weaker than a live-migration probe.
				SchedulerHints: map[string]any{
					"_nova_check_type": "evacuate",
				},
				Flavor: api.NovaObject[api.NovaFlavor]{
					Data: api.NovaFlavor{
						Name:       seed.Flavor.Name,
						ExtraSpecs: seed.Flavor.ExtraSpecs,
						MemoryMB:   seed.Flavor.MemoryMB,
						VCPUs:      seed.Flavor.VCPUs,
					},
				},
				Image: api.NovaObject[api.NovaImageMeta]{
					Data: api.NovaImageMeta{
						Properties: api.NovaObject[map[string]any]{
							Data: imageProps,
						},
					},
				},
			},
		},
	}
	log.V(1).Info("external scheduler request prepared", "request", requestObj)

	reqBody, err := json.Marshal(requestObj)
	if err != nil {
		log.Error(err, "failed to marshal external scheduler request")
		return nil, fmt.Errorf("failed to marshal external scheduler request: %w", err)
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost,
		f.schedulerURL, bytes.NewReader(reqBody))
	if err != nil {
		log.Error(err, "failed to create HTTP request")
		return nil, fmt.Errorf("failed to create HTTP request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")

	startTime := time.Now()
	response, err := f.httpClient.Do(httpReq)
	duration := time.Since(startTime)
	if err != nil {
		f.monitor.RecordSchedulerRequest("error", duration)
		log.Error(err, "failed to send external scheduler request")
		return nil, fmt.Errorf("failed to send external scheduler request: %w", err)
	}
	defer response.Body.Close()
	f.monitor.RecordSchedulerRequest(strconv.Itoa(response.StatusCode), duration)
	log.V(1).Info("external scheduler request sent",
		"duration", duration, "status", response.StatusCode)

	if response.StatusCode != http.StatusOK {
		log.Error(nil, "external scheduler request failed", "status", response.StatusCode)
		return nil, fmt.Errorf("external scheduler request failed with status: %d", response.StatusCode)
	}

	var responseObj api.ExternalSchedulerResponse
	if err := json.NewDecoder(response.Body).Decode(&responseObj); err != nil {
		log.Error(err, "failed to decode external scheduler response")
		return nil, fmt.Errorf("failed to decode external scheduler response: %w", err)
	}
	log.V(1).Info("external scheduler response received", "response", responseObj)

	candidates := []hv1.Hypervisor{}
	for _, hv := range hvsByName {
		if slices.Contains(responseObj.Hosts, hv.Name) {
			candidates = append(candidates, hv)
		}
	}
	return candidates, nil
}
