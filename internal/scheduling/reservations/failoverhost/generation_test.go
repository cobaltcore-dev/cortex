// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package failoverhost

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"slices"
	"testing"

	api "github.com/cobaltcore-dev/cortex/api/external/nova"
	"github.com/cobaltcore-dev/cortex/api/v1alpha1"
	"github.com/cobaltcore-dev/cortex/internal/knowledge/extractor/plugins/compute"
	hv1 "github.com/cobaltcore-dev/openstack-hypervisor-operator/api/v1"
	corev1 "k8s.io/api/core/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// fakeSeeder returns a fixed set of seeds, so generator tests drive the pipeline
// without reading the flavor-groups Knowledge CRD.
type fakeSeeder struct{ seeds []Seed }

func (f fakeSeeder) Seeds(context.Context, topology) ([]Seed, error) { return f.seeds, nil }

// fakePartitionBuilder returns a fixed set of partitions regardless of input, so
// a test can drive Generate with overlapping cliques the real builder would
// produce.
type fakePartitionBuilder struct{ partitions []Partition }

func (f fakePartitionBuilder) BuildPartitions([]Path) []Partition {
	return f.partitions
}

// TestGenerateProducesReservations exercises the whole pipeline end-to-end
// against a fake cluster and a stub external scheduler: collect -> Seeds ->
// FindPaths (via HTTP) -> BuildPartitions -> PlanReservations. It asserts the
// generator turns a pair of mutually-evacuable hosts into a desired failover
// host reservation.
func TestGenerateProducesReservations(t *testing.T) {
	// Two equally-sized hosts. A single flavor seed is accepted on both, so the
	// path finder discovers the mutual edges hvA<->hvB and they form one partition.
	hvA := buildHV("hvA", 64, 32)
	hvB := buildHV("hvB", 64, 32)

	finder, server := stubScheduler(t, map[string][]string{
		"flavor": {"hvA", "hvB"},
	}, nil)
	defer server.Close()

	g := &pipelineGenerator{
		Client:             newTestClient(t, hvA, hvB),
		seeder:             fakeSeeder{seeds: []Seed{seed("flavor")}},
		pathFinder:         finder,
		partitionBuilder:   cliquePartitionBuilder{},
		reservationPlanner: ratioReservationPlanner{ratio: 0.5, minimum: 1},
	}
	desired, _, ok := g.Generate(context.Background())
	if !ok {
		t.Fatalf("Generate returned ok=false")
	}
	if len(desired) == 0 {
		t.Fatalf("expected at least one desired reservation, got none")
	}
	for _, r := range desired {
		if r.Spec.Type != v1alpha1.ReservationTypeFailoverHost {
			t.Errorf("unexpected reservation type %q", r.Spec.Type)
		}
		if r.Spec.AvailabilityZone != "az1" {
			t.Errorf("availability zone = %q, want az1", r.Spec.AvailabilityZone)
		}
	}
}

// TestGenerateDeduplicatesSharedHost checks that when overlapping maximal
// cliques share a host (hvB in both {hvA,hvB} and {hvB,hvC}), Generate assigns
// that host to exactly one partition before planning, so planning never sees hvB
// in two partitions and cannot emit two reservations with the same name. The two
// partitions are equal size, so the deterministic tiebreak keeps hvB in the one
// with the smaller first host name ({hvA,hvB}); the other shrinks to the single
// host hvC, too small to reserve. The surviving {hvA,hvB} partition reserves one
// target (hvA, the first equal-cost host), whose only source host is hvB — proof
// hvB was planned once, as a source, not as a conflicting second target.
func TestGenerateDeduplicatesSharedHost(t *testing.T) {
	hvA := buildHV("hvA", 64, 32)
	hvB := buildHV("hvB", 64, 32)
	hvC := buildHV("hvC", 64, 32)

	// The path finder's edges are irrelevant here: the fake builder ignores them
	// and hands Generate the overlapping cliques directly. The stub still needs an
	// answer so FindPaths succeeds; one seed accepted on all three suffices.
	finder, server := stubScheduler(t, map[string][]string{
		"flavor": {"hvA", "hvB", "hvC"},
	}, nil)
	defer server.Close()

	g := &pipelineGenerator{
		Client:     newTestClient(t, hvA, hvB, hvC),
		seeder:     fakeSeeder{seeds: []Seed{seed("flavor")}},
		pathFinder: finder,
		partitionBuilder: fakePartitionBuilder{partitions: []Partition{
			{Hosts: []hv1.Hypervisor{*hvA, *hvB}}, {Hosts: []hv1.Hypervisor{*hvB, *hvC}},
		}},
		reservationPlanner: ratioReservationPlanner{ratio: 0.5, minimum: 1},
	}
	desired, _, ok := g.Generate(context.Background())
	if !ok {
		t.Fatalf("Generate returned ok=false")
	}
	// Exactly one reservation survives: {hvA,hvB} reserves hvA, whose source host
	// is the deduped hvB. {hvB,hvC} lost hvB and shrank to lone hvC, reserving
	// nothing.
	if len(desired) != 1 {
		t.Fatalf("desired reservations = %d, want 1", len(desired))
	}
	aRes, ok := desired[corev1.ObjectReference{Name: "failover-host-hvA"}]
	if !ok {
		t.Fatalf("expected a reservation targeting hvA")
	}
	if got := aRes.Spec.FailoverHostReservation.SourceHosts; len(got) != 1 || got[0] != "hvB" {
		t.Errorf("hvA source hosts = %v, want [hvB]", got)
	}
}

// TestGenerateSharedHostKeptInSmallestPartition checks that when a shared host
// sits in partitions of different sizes, Generate assigns it to the smallest
// one, keeping the most partitions alive. Here hvC is shared between
// {hvA,hvB,hvC} (size 3) and {hvC,hvD} (size 2). Keeping hvC in the smaller
// {hvC,hvD} lets that partition survive, while {hvA,hvB,hvC} merely shrinks to
// {hvA,hvB} and stays above the two-host minimum — so both partitions retain a
// failover target. Keeping hvC in the larger partition instead would collapse
// {hvC,hvD} to a lone host and leave hvD unprotected.
func TestGenerateSharedHostKeptInSmallestPartition(t *testing.T) {
	hvA := buildHV("hvA", 64, 32)
	hvB := buildHV("hvB", 64, 32)
	hvC := buildHV("hvC", 64, 32)
	hvD := buildHV("hvD", 64, 32)

	finder, server := stubScheduler(t, map[string][]string{
		"flavor": {"hvA", "hvB", "hvC", "hvD"},
	}, nil)
	defer server.Close()

	g := &pipelineGenerator{
		Client:     newTestClient(t, hvA, hvB, hvC, hvD),
		seeder:     fakeSeeder{seeds: []Seed{seed("flavor")}},
		pathFinder: finder,
		partitionBuilder: fakePartitionBuilder{partitions: []Partition{
			{Hosts: []hv1.Hypervisor{*hvA, *hvB, *hvC}}, {Hosts: []hv1.Hypervisor{*hvC, *hvD}},
		}},
		reservationPlanner: ratioReservationPlanner{ratio: 0.5, minimum: 1},
	}
	desired, _, ok := g.Generate(context.Background())
	if !ok {
		t.Fatalf("Generate returned ok=false")
	}
	// Both partitions survive: {hvC,hvD} keeps the shared hvC and {hvA,hvB,hvC}
	// shrinks to {hvA,hvB}. Each reserves one target, so the shared host did not
	// cost either partition its coverage.
	if len(desired) != 2 {
		t.Fatalf("desired reservations = %d, want 2", len(desired))
	}
	// {hvC,hvD} must still exist, proving hvC stayed in the smaller partition.
	// Whichever of hvC/hvD is reserved, its sole source host is the other.
	cdReserved := false
	for name, r := range desired {
		sources := r.Spec.FailoverHostReservation.SourceHosts
		if name.Name == "failover-host-hvC" && len(sources) == 1 && sources[0] == "hvD" {
			cdReserved = true
		}
		if name.Name == "failover-host-hvD" && len(sources) == 1 && sources[0] == "hvC" {
			cdReserved = true
		}
	}
	if !cdReserved {
		t.Errorf("expected a {hvC,hvD} reservation, got %v", desired)
	}
}

// TestGenerateAbortsOnMissingAvailabilityZone checks that collect rejects a torn
// snapshot: a hypervisor without an availability-zone label makes Generate
// return ok=false so the pass is abandoned rather than seeding from an
// inconsistent host set.
func TestGenerateAbortsOnMissingAvailabilityZone(t *testing.T) {
	hvA := buildHV("hvA", 64, 32)
	hvA.Labels = nil // strip the availability-zone label

	g := &pipelineGenerator{
		Client:             newTestClient(t, hvA),
		seeder:             fakeSeeder{seeds: []Seed{seed("flavor")}},
		partitionBuilder:   cliquePartitionBuilder{},
		reservationPlanner: ratioReservationPlanner{ratio: 0.5, minimum: 1},
	}
	if _, _, ok := g.Generate(context.Background()); ok {
		t.Fatalf("expected Generate to abort (ok=false) on a hypervisor with no AZ")
	}
}

// ============================================================================
// Whole-host guarantee scenarios
//
// These exercise the whole failover-host generation pipeline (seeder ->
// path finder -> clique builder -> planner) end to end against a *stubbed*
// scheduler whose per-(flavor, availability zone) accept-sets stand in for what
// the real Nova pipeline would answer. The accept-sets encode the compatibility
// axes the scenarios care about (traits, availability zone); that the real
// filters actually produce those answers is covered by each filter's own unit
// tests (filter_correct_az, filter_has_requested_traits, filter_has_enough_capacity).
//
// The invariant under test is the ADR 0011 whole-host guarantee: a reserved
// failover target must be able to absorb the entire workload of every host it is
// recorded as rescuing — i.e. the target must accept every flavor its source
// hosts accept.
// ============================================================================

const traitX = "CUSTOM_TRAIT_X"

func TestWholeHostGuarantee(t *testing.T) {
	tests := []struct {
		name        string
		hypervisors []*hv1.Hypervisor
		flavors     []compute.FlavorInGroup
		// acceptsByFlavorAZ[flavor][az] is the set of hosts the stub scheduler
		// returns for a seed of that flavor in that availability zone — the
		// compatibility the real pipeline would compute. Capacity fit is NOT
		// modeled here; the path finder applies the real whole-host size check on
		// top, so an undersized host can still be dropped despite accepting.
		acceptsByFlavorAZ map[string]map[string][]string
		ratio             float64
		minimum           int
		// wantReservations, when non-zero, asserts the exact reservation count.
		wantReservations int
		expectAny        bool
		forbiddenTargets []string
		expectSiblings   bool
	}{
		{
			// A single shared flavor forges a rescue link between every host, but a
			// CUSTOM_TRAIT_X VM could never restart on a basic host (the scheduler
			// rejects traitx there). The two special hosts form a valid trait-X
			// partition and the two basics a general one; the same-flavor-set clique
			// check must not merge them, or a basic host gets reserved as the
			// specials' target and the whole-host guarantee breaks.
			name: "trait-incompatible host is not reserved as a failover target",
			hypervisors: []*hv1.Hypervisor{
				hvTraits("basic-1"), hvTraits("basic-2"),
				hvTraits("special-1", traitX), hvTraits("special-2", traitX),
			},
			flavors: []compute.FlavorInGroup{traitFlavor("general", ""), traitFlavor("traitx", traitX)},
			acceptsByFlavorAZ: map[string]map[string][]string{
				"general": {"az1": {"basic-1", "basic-2", "special-1", "special-2"}},
				"traitx":  {"az1": {"special-1", "special-2"}},
			},
			ratio: 0.1, minimum: 1, expectAny: true,
		},
		{
			// Three identical hosts accept the one flavor, so whichever becomes the
			// target trivially satisfies the invariant. Guards against the check
			// above being vacuously true.
			name:        "homogeneous partition reserves a compatible target",
			hypervisors: []*hv1.Hypervisor{hvTraits("hv-1"), hvTraits("hv-2"), hvTraits("hv-3")},
			flavors:     []compute.FlavorInGroup{traitFlavor("general", "")},
			acceptsByFlavorAZ: map[string]map[string][]string{
				"general": {"az1": {"hv-1", "hv-2", "hv-3"}},
			},
			expectAny: true,
		},
		{
			// Two large hosts and one small host all accept the flavor, but an
			// evacuation moves a whole host's workload at once, so a large host's
			// VMs could never fit on the small one. The path finder's whole-host fit
			// check must drop every large->small edge, leaving "small" only ever a
			// source, never a target — so it must never be reserved.
			name: "undersized host is never reserved as a failover target",
			hypervisors: []*hv1.Hypervisor{
				buildHV("large-1", 128, 64), buildHV("large-2", 128, 64), buildHV("small-1", 16, 8),
			},
			flavors: []compute.FlavorInGroup{traitFlavor("general", "")},
			acceptsByFlavorAZ: map[string]map[string][]string{
				"general": {"az1": {"large-1", "large-2", "small-1"}},
			},
			expectAny: true, forbiddenTargets: []string{"small-1"},
		},
		{
			// Two hosts per zone all accept the flavor, but the AZ filter only admits
			// hosts of the seed's own zone, so each zone forms its own partition and
			// the fleet yields exactly two reservations — a wrong cross-AZ merge would
			// cap at one three-host partition's worth instead.
			name: "hosts in different AZs are not merged into one partition",
			hypervisors: []*hv1.Hypervisor{
				hvInZone("az1-1", "az1"), hvInZone("az1-2", "az1"),
				hvInZone("az2-1", "az2"), hvInZone("az2-2", "az2"),
			},
			flavors: []compute.FlavorInGroup{traitFlavor("general", "")},
			acceptsByFlavorAZ: map[string]map[string][]string{
				"general": {
					"az1": {"az1-1", "az1-2"},
					"az2": {"az2-1", "az2-2"},
				},
			},
			expectAny: true, wantReservations: 2,
		},
		{
			// A ratio of 1.0 would reserve the whole partition, but the
			// never-reserve-all rule caps a three-host partition at two targets. Each
			// reservation must then list the other reserved host among its sources,
			// the post-failover shortcut a multi-target partition relies on.
			name:        "multi-target partition records sibling reserved hosts as sources",
			hypervisors: []*hv1.Hypervisor{hvTraits("hv-1"), hvTraits("hv-2"), hvTraits("hv-3")},
			flavors:     []compute.FlavorInGroup{traitFlavor("general", "")},
			acceptsByFlavorAZ: map[string]map[string][]string{
				"general": {"az1": {"hv-1", "hv-2", "hv-3"}},
			},
			ratio: 1.0, minimum: 1, expectAny: true, wantReservations: 2, expectSiblings: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			objs := make([]client.Object, 0, len(tt.hypervisors)+1)
			hvsByName := make(map[string]hv1.Hypervisor, len(tt.hypervisors))
			for _, hv := range tt.hypervisors {
				objs = append(objs, hv)
				hvsByName[hv.Name] = *hv
			}
			objs = append(objs, flavorGroupsKnowledge(t, true, tt.flavors...))

			finder, server := azAwareStubScheduler(t, tt.acceptsByFlavorAZ)
			defer server.Close()

			ratio, minimum := tt.ratio, tt.minimum
			if ratio == 0 {
				ratio = 1.0
			}
			if minimum == 0 {
				minimum = 1
			}
			g := &pipelineGenerator{
				Client:             newTestClient(t, objs...),
				seeder:             &seeder{k8s: newTestClient(t, objs...)},
				pathFinder:         finder,
				partitionBuilder:   cliquePartitionBuilder{},
				reservationPlanner: ratioReservationPlanner{ratio: ratio, minimum: minimum},
			}
			desired, _, ok := g.Generate(context.Background())
			if !ok {
				t.Fatal("Generate returned ok=false")
			}
			if tt.expectAny && len(desired) == 0 {
				t.Fatal("expected at least one failover host reservation, got none")
			}
			if tt.wantReservations != 0 && len(desired) != tt.wantReservations {
				t.Fatalf("expected %d reservations, got %d", tt.wantReservations, len(desired))
			}
			for _, res := range desired {
				if slices.Contains(tt.forbiddenTargets, res.Spec.TargetHost) {
					t.Errorf("host %q was reserved as a failover target but must never be", res.Spec.TargetHost)
				}
			}
			verifyWholeHostEvacuable(t, desired, tt.acceptsByFlavorAZ)
			if tt.expectSiblings {
				verifySiblingSources(t, desired)
			}
		})
	}
}

// verifyWholeHostEvacuable asserts the ADR 0011 invariant: for every reservation,
// the target host accepts every flavor each of its recorded source hosts accepts.
func verifyWholeHostEvacuable(t *testing.T, desired map[corev1.ObjectReference]v1alpha1.Reservation, acceptsByFlavorAZ map[string]map[string][]string) {
	t.Helper()
	// Invert the accept-sets into per-host flavor sets, across all AZs.
	acceptsByHost := map[string]map[string]bool{}
	for flavor, byAZ := range acceptsByFlavorAZ {
		for _, hosts := range byAZ {
			for _, h := range hosts {
				if acceptsByHost[h] == nil {
					acceptsByHost[h] = map[string]bool{}
				}
				acceptsByHost[h][flavor] = true
			}
		}
	}
	for _, res := range desired {
		target := res.Spec.TargetHost
		if res.Spec.FailoverHostReservation == nil {
			t.Fatalf("reservation %s has no FailoverHostReservation spec", res.Name)
		}
		for _, source := range res.Spec.FailoverHostReservation.SourceHosts {
			for flavor := range acceptsByHost[source] {
				if !acceptsByHost[target][flavor] {
					t.Errorf("reservation %s: target %q cannot run flavor %q that source %q accepts — %q's VMs could not evacuate onto it",
						res.Name, target, flavor, source, source)
				}
			}
		}
	}
}

// verifySiblingSources asserts that in a partition reserving more than one
// failover host, every reservation lists each other reserved host among its
// source hosts (the post-failover shortcut the planner relies on).
func verifySiblingSources(t *testing.T, desired map[corev1.ObjectReference]v1alpha1.Reservation) {
	t.Helper()
	targets := make([]string, 0, len(desired))
	for _, res := range desired {
		targets = append(targets, res.Spec.TargetHost)
	}
	for _, res := range desired {
		for _, sibling := range targets {
			if sibling == res.Spec.TargetHost {
				continue
			}
			if !slices.Contains(res.Spec.FailoverHostReservation.SourceHosts, sibling) {
				t.Errorf("reservation for target %q does not list sibling reserved host %q as a source",
					res.Spec.TargetHost, sibling)
			}
		}
	}
}

// azAwareStubScheduler serves a scheduler that answers each probe with the
// accept-set for the request's flavor *and* availability zone, so a scenario can
// model the AZ filter (a seed in az1 only ever accepts az1 hosts). It performs
// the same request-shape assertions as the simpler stubScheduler.
func azAwareStubScheduler(t *testing.T, acceptsByFlavorAZ map[string]map[string][]string) (*evacuationPathFinder, *httptest.Server) {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req api.ExternalSchedulerRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Errorf("decode request: %v", err)
		}
		if !req.Options.AssumeEmptyHosts {
			t.Errorf("request for flavor %q must set AssumeEmptyHosts", req.Spec.Data.Flavor.Data.Name)
		}
		if intent, err := req.GetIntent(); err != nil || intent != api.EvacuateIntent {
			t.Errorf("request for flavor %q must use evacuate intent, got %v (err=%v)", req.Spec.Data.Flavor.Data.Name, intent, err)
		}
		flavor := req.Spec.Data.Flavor.Data.Name
		az := req.Spec.Data.AvailabilityZone
		if err := json.NewEncoder(w).Encode(api.ExternalSchedulerResponse{Hosts: acceptsByFlavorAZ[flavor][az]}); err != nil {
			t.Errorf("encode response: %v", err)
		}
	}))
	finder := &evacuationPathFinder{httpClient: server.Client(), schedulerURL: server.URL}
	return finder, server
}

// hvTraits builds a fixed-size (64Gi/32cpu) hypervisor in az1 with the given
// traits, so a scenario isolates trait (not capacity) as the compatibility axis.
func hvTraits(name string, traits ...string) *hv1.Hypervisor {
	h := buildHV(name, 64, 32)
	h.Status.Traits = traits
	return h
}

// hvInZone builds a fixed-size hypervisor (matching hvTraits) in the given
// availability zone, so a scenario can exercise AZ partitioning.
func hvInZone(name, az string) *hv1.Hypervisor {
	h := buildHV(name, 64, 32)
	h.Labels[corev1.LabelTopologyZone] = az
	return h
}

// traitFlavor builds a generic flavor requiring the given trait (or none). Its
// hypervisor type is a supported value so the seeder keeps it.
func traitFlavor(name, requiredTrait string) compute.FlavorInGroup {
	extra := map[string]string{"capabilities:hypervisor_type": "ch"}
	if requiredTrait != "" {
		extra["trait:"+requiredTrait] = "required"
	}
	return compute.FlavorInGroup{Name: name, VCPUs: 1, MemoryMB: 1024, ExtraSpecs: extra}
}
