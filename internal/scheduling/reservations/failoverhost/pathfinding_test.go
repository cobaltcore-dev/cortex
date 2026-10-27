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
	hv1 "github.com/cobaltcore-dev/openstack-hypervisor-operator/api/v1"
)

// stubScheduler returns a path finder wired to an httptest server whose
// per-flavor answers come from candidatesByFlavor (keyed by the seed's flavor
// name); a flavor in errFlavors gets an HTTP 500. The handler asserts the request
// carries valid options, a non-empty host set, a kvm img_hv_type, and evacuation
// intent with no source_host — mirroring what the new seeded probe sends, so a
// regression that drops any of them is caught here. The caller closes the server.
func stubScheduler(t *testing.T, candidatesByFlavor map[string][]string, errFlavors map[string]bool) (*evacuationPathFinder, *httptest.Server) {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req api.ExternalSchedulerRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Errorf("decode request: %v", err)
		}
		if err := req.Options.Validate(); err != nil {
			t.Errorf("invalid scheduling options: %v", err)
		}
		if !req.Options.AssumeEmptyHosts {
			// A failover target is reserved empty, so candidates must be judged by
			// total capacity. Dropping this would orphan every busy host.
			t.Errorf("request for flavor %q must set AssumeEmptyHosts", req.Spec.Data.Flavor.Data.Name)
		}
		if len(req.Hosts) == 0 {
			t.Errorf("request for flavor %q has no candidate hosts", req.Spec.Data.Flavor.Data.Name)
		}
		if got := req.Spec.Data.Image.Data.Properties.Data["img_hv_type"]; got != string(api.NovaImageMetaHVTypeKVM) {
			// The hypervisor-type filter needs img_hv_type to judge flavor/image
			// compatibility; dropping it would make that filter misbehave.
			t.Errorf("request for flavor %q must set img_hv_type=%q, got %q", req.Spec.Data.Flavor.Data.Name, api.NovaImageMetaHVTypeKVM, got)
		}
		// The probe uses evacuation intent (cold migration) and sets no source_host:
		// a failover evacuates rather than live-migrates, so the live-migratability
		// feature check is deliberately skipped. Catch a regression to the stricter
		// live-migration probe here.
		if intent, err := req.GetIntent(); err != nil || intent != api.EvacuateIntent {
			t.Errorf("request for flavor %q must use evacuate intent, got %v (err=%v)", req.Spec.Data.Flavor.Data.Name, intent, err)
		}
		if _, ok := req.Spec.Data.SchedulerHints["source_host"]; ok {
			t.Errorf("request for flavor %q must not set source_host", req.Spec.Data.Flavor.Data.Name)
		}
		flavor := req.Spec.Data.Flavor.Data.Name
		if errFlavors[flavor] {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		if err := json.NewEncoder(w).Encode(api.ExternalSchedulerResponse{Hosts: candidatesByFlavor[flavor]}); err != nil {
			t.Errorf("encode response: %v", err)
		}
	}))
	finder := &evacuationPathFinder{httpClient: server.Client(), schedulerURL: server.URL}
	return finder, server
}

// pathNames collapses failover edges into sorted "source->target" strings, for
// order-independent comparison.
func pathNames(paths [][2]hv1.Hypervisor) []string {
	out := make([]string, 0, len(paths))
	for _, p := range paths {
		out = append(out, p[0].Name+"->"+p[1].Name)
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

// seed is a tiny constructor for a flavor seed named name in az1.
func seed(name string) Seed {
	return Seed{Flavor: Flavor{Name: name}, AvailabilityZone: "az1"}
}

func TestFindPathsErrorsOnLookupFailure(t *testing.T) {
	// A seed's lookup fails with a scheduler error. That's an infrastructure
	// failure, not an answer that the seed has no candidates, so FindPaths must
	// return an error to abort the pass rather than silently dropping paths —
	// otherwise the caller would publish an empty desired state and prune valid
	// reservations.
	finder, server := stubScheduler(t,
		map[string][]string{"good": {"a", "b"}},
		map[string]bool{"bad": true},
	)
	defer server.Close()

	// "bad" is probed and errors; the pass must abort regardless of "good".
	topo := hostTopo(map[string]int64{"a": 32, "b": 32})
	if _, err := finder.FindPaths(context.Background(), topo, []Seed{seed("bad"), seed("good")}); err == nil {
		t.Fatal("FindPaths: expected error on scheduler lookup failure, got nil")
	}
}

func TestFindPathsPairsCoAcceptingHosts(t *testing.T) {
	// A single seed accepted on a, b and c makes all three mutually compatible.
	// Equal-sized hosts yield edges both ways between every pair.
	finder, server := stubScheduler(t, map[string][]string{
		"flavor": {"a", "b", "c"},
	}, nil)
	defer server.Close()

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
	finder, server := stubScheduler(t, map[string][]string{
		"flavor": {"big", "small", "equal"},
	}, nil)
	defer server.Close()

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
	finder, server := stubScheduler(t, map[string][]string{
		"flavorA": {"hvA", "hvB"},
		"flavorB": {"hvA", "hvB"},
	}, nil)
	defer server.Close()

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
	finder, server := stubScheduler(t, map[string][]string{
		"flavor": {"only"},
	}, nil)
	defer server.Close()

	topo := hostTopo(map[string]int64{"only": 64, "other": 64})
	got, err := finder.FindPaths(context.Background(), topo, []Seed{seed("flavor")})
	if err != nil {
		t.Fatalf("FindPaths: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("paths = %v, want none", pathNames(got))
	}
}
