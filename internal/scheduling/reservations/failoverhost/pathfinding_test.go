// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package failoverhost

import (
	"context"
	"fmt"
	"slices"
	"testing"

	api "github.com/cobaltcore-dev/cortex/api/external/nova"
	"github.com/cobaltcore-dev/cortex/internal/scheduling/compute"
	"github.com/cobaltcore-dev/cortex/internal/scheduling/lib"
	hv1 "github.com/cobaltcore-dev/openstack-hypervisor-operator/api/v1"
)

// stubScheduler returns a path finder wired to an in-process scheduler whose
// per-flavor answers come from candidatesByFlavor (keyed by the seed's flavor
// name); a flavor in errFlavors makes the probe fail. The fake pipeline asserts
// the request carries valid options, a non-empty host set, AssumeEmptyHosts, a
// KVM image hypervisor type, and evacuation intent with no source host —
// mirroring what the seeded probe sends, so a regression that drops any of them
// is caught here.
func stubScheduler(t *testing.T, candidatesByFlavor map[string][]string, errFlavors map[string]bool) *evacuationPathFinder {
	t.Helper()
	provider := &stubPipelineProvider{pipeline: &stubPipeline{
		t:                  t,
		candidatesByFlavor: candidatesByFlavor,
		errFlavors:         errFlavors,
	}}
	scheduler := compute.NewPlacement(provider, passthroughGatherer{})
	return &evacuationPathFinder{scheduler: scheduler}
}

// stubPipelineProvider serves the same stub pipeline for every pipeline name the
// probe infers (always a KVM pipeline here).
type stubPipelineProvider struct {
	pipeline lib.FilterWeigherPipeline[compute.PlacementRequest]
}

// passthroughGatherer is a compute.CandidateGatherer that leaves the request's
// preselected hosts unchanged. The tests seed their own eligible hosts, so
// gathering is a no-op here.
type passthroughGatherer struct{}

func (passthroughGatherer) MutateWithAllCandidates(context.Context, *compute.PlacementRequest) error {
	return nil
}

func (p *stubPipelineProvider) GetPipeline(string) (lib.FilterWeigherPipeline[compute.PlacementRequest], bool) {
	return p.pipeline, true
}

// stubPipeline is a fake filter/weigher pipeline that validates the probe's
// request shape and returns the preconfigured candidate hosts for the request's
// flavor, mirroring the old httptest stub.
type stubPipeline struct {
	t                  *testing.T
	candidatesByFlavor map[string][]string
	errFlavors         map[string]bool
}

func (s *stubPipeline) Run(req compute.PlacementRequest) (compute.PlacementResult, error) {
	// Identify which seed this probe is for by the trait the seed constructor
	// puts on the flavor (see seed()), which the probe carries in RequiredTraits.
	flavor := ""
	if len(req.RequiredTraits) > 0 {
		flavor = req.RequiredTraits[0]
	}
	if err := req.Options.Validate(); err != nil {
		s.t.Errorf("invalid scheduling options: %v", err)
	}
	if !req.Options.AssumeEmptyHosts {
		// A failover target is reserved empty, so candidates must be judged by
		// total capacity. Dropping this would orphan every busy host.
		s.t.Errorf("request for flavor %q must set AssumeEmptyHosts", flavor)
	}
	if len(req.Hosts) == 0 {
		s.t.Errorf("request for flavor %q has no candidate hosts", flavor)
	}
	if req.ImageHypervisorType != api.ImageHypervisorTypeKVM {
		// The hypervisor-type filter needs the image hypervisor type to judge
		// flavor/image compatibility; dropping it would make that filter misbehave.
		s.t.Errorf("request for flavor %q must set ImageHypervisorType=%q, got %q", flavor, api.ImageHypervisorTypeKVM, req.ImageHypervisorType)
	}
	// The probe uses evacuation intent (cold migration) and sets no source host:
	// a failover evacuates rather than live-migrates, so the live-migratability
	// feature check is deliberately skipped. Catch a regression to the stricter
	// live-migration probe here.
	if req.Intent != api.EvacuateIntent {
		s.t.Errorf("request for flavor %q must use evacuate intent, got %v", flavor, req.Intent)
	}
	if req.SourceHost != "" {
		s.t.Errorf("request for flavor %q must not set a source host", flavor)
	}
	if s.errFlavors[flavor] {
		return compute.PlacementResult{}, fmt.Errorf("probe failed for flavor %q", flavor)
	}
	return compute.PlacementResult{OrderedHosts: s.candidatesByFlavor[flavor]}, nil
}

// pathNames collapses failover edges into sorted "source->target" strings, for
// order-independent comparison.
func pathNames(paths []Path) []string {
	out := make([]string, 0, len(paths))
	for _, p := range paths {
		out = append(out, p.Source.Name+"->"+p.Target.Name)
	}
	slices.Sort(out)
	return out
}

// hostTopo builds a topology of the named hosts (each sized memGi GiB), so
// FindPaths can be exercised without a k8s client. Hosts carry az1 so a seed in
// az1 lines up with them.
func hostTopo(hostMemGi map[string]int64) topology {
	hypervisors := map[string]hv1.Hypervisor{}
	for name, memGi := range hostMemGi {
		hypervisors[name] = *buildHV(name, memGi, 1)
	}
	return topology{hypervisors: hypervisors}
}

// seed is a tiny constructor for a flavor seed named name in az1. The name is
// also set as a required trait so the stub pipeline can tell which seed a probe
// request belongs to (the probe carries the flavor's traits in RequiredTraits).
func seed(name string) Seed {
	return Seed{
		Flavor:           Flavor{Name: name, ExtraSpecs: map[string]string{"trait:" + name: "required"}},
		AvailabilityZone: "az1",
	}
}

func TestFindPathsErrorsOnLookupFailure(t *testing.T) {
	// A seed's lookup fails with a scheduler error. That's an infrastructure
	// failure, not an answer that the seed has no candidates, so FindPaths must
	// return an error to abort the pass rather than silently dropping paths —
	// otherwise the caller would publish an empty desired state and prune valid
	// reservations.
	finder := stubScheduler(t,
		map[string][]string{"good": {"a", "b"}},
		map[string]bool{"bad": true},
	)

	// "bad" is probed and errors; the pass must abort regardless of "good".
	topo := hostTopo(map[string]int64{"a": 32, "b": 32})
	if _, err := finder.FindPaths(context.Background(), topo, []Seed{seed("bad"), seed("good")}); err == nil {
		t.Fatal("FindPaths: expected error on scheduler lookup failure, got nil")
	}
}

func TestFindPathsPairsCoAcceptingHosts(t *testing.T) {
	// A single seed accepted on a, b and c makes all three mutually compatible.
	// Equal-sized hosts yield edges both ways between every pair.
	finder := stubScheduler(t, map[string][]string{
		"flavor": {"a", "b", "c"},
	}, nil)

	topo := hostTopo(map[string]int64{"a": 32, "b": 32, "c": 32})
	got, err := finder.FindPaths(context.Background(), topo, []Seed{seed("flavor")})
	if err != nil {
		t.Fatalf("FindPaths: %v", err)
	}
	want := []string{"a->b", "a->c", "b->a", "b->c", "c->a", "c->b"}
	if !slices.Equal(pathNames(got), want) {
		t.Errorf("paths = %v, want %v", pathNames(got), want)
	}
}

func TestFindPathsDropsTargetSmallerThanSource(t *testing.T) {
	// A seed is accepted on "big" (64Gi), "small" (16Gi) and "equal" (64Gi). The
	// gang-fit constraint keeps an edge only toward a target at least as large as
	// the source, so no edge points at "small", and big<->equal are mutual.
	finder := stubScheduler(t, map[string][]string{
		"flavor": {"big", "small", "equal"},
	}, nil)

	topo := hostTopo(map[string]int64{"big": 64, "small": 16, "equal": 64})
	got, err := finder.FindPaths(context.Background(), topo, []Seed{seed("flavor")})
	if err != nil {
		t.Fatalf("FindPaths: %v", err)
	}
	// small can reach the larger big/equal (and vice versa is dropped); big and
	// equal are mutual. Nothing points at small.
	want := []string{"big->equal", "equal->big", "small->big", "small->equal"}
	if !slices.Equal(pathNames(got), want) {
		t.Errorf("paths = %v, want %v", pathNames(got), want)
	}
}

func TestFindPathsDeduplicatesAcrossSeeds(t *testing.T) {
	// Two seeds both accepted on the same pair of equal hosts. Each directed edge
	// must appear once, not once per seed.
	finder := stubScheduler(t, map[string][]string{
		"flavorA": {"hvA", "hvB"},
		"flavorB": {"hvA", "hvB"},
	}, nil)

	topo := hostTopo(map[string]int64{"hvA": 64, "hvB": 64})
	got, err := finder.FindPaths(context.Background(), topo, []Seed{seed("flavorA"), seed("flavorB")})
	if err != nil {
		t.Fatalf("FindPaths: %v", err)
	}
	if want := []string{"hvA->hvB", "hvB->hvA"}; !slices.Equal(pathNames(got), want) {
		t.Errorf("paths = %v, want %v", pathNames(got), want)
	}
}

func TestFindPathsSingleCandidateYieldsNoEdge(t *testing.T) {
	// A seed accepted on only one host can form no pair, so contributes no edge.
	finder := stubScheduler(t, map[string][]string{
		"flavor": {"only"},
	}, nil)

	topo := hostTopo(map[string]int64{"only": 64, "other": 64})
	got, err := finder.FindPaths(context.Background(), topo, []Seed{seed("flavor")})
	if err != nil {
		t.Fatalf("FindPaths: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("paths = %v, want none", pathNames(got))
	}
}

func TestFindPathsRecordsSupportingFlavors(t *testing.T) {
	// flavorA is accepted on both hosts, flavorB only on hvA. The edge hvA<->hvB is
	// produced solely by flavorA, so each directed edge must carry exactly
	// ["flavorA"] — flavorB co-accepts on no pair and leaves no trace.
	finder := stubScheduler(t, map[string][]string{
		"flavorA": {"hvA", "hvB"},
		"flavorB": {"hvA"},
	}, nil)

	topo := hostTopo(map[string]int64{"hvA": 64, "hvB": 64})
	got, err := finder.FindPaths(context.Background(), topo, []Seed{seed("flavorA"), seed("flavorB")})
	if err != nil {
		t.Fatalf("FindPaths: %v", err)
	}
	for _, p := range got {
		if want := []string{"flavorA"}; !slices.Equal(p.Flavors, want) {
			t.Errorf("edge %s->%s flavors = %v, want %v", p.Source.Name, p.Target.Name, p.Flavors, want)
		}
	}
	if want := []string{"hvA->hvB", "hvB->hvA"}; !slices.Equal(pathNames(got), want) {
		t.Errorf("paths = %v, want %v", pathNames(got), want)
	}
}
