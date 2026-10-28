// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package failoverhost

import (
	"context"
	"testing"

	"github.com/cobaltcore-dev/cortex/api/v1alpha1"
	hv1 "github.com/cobaltcore-dev/openstack-hypervisor-operator/api/v1"
	corev1 "k8s.io/api/core/v1"
)

// fakeSeeder returns a fixed set of seeds, so generator tests drive the pipeline
// without a Nova client.
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
