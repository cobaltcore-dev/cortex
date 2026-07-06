// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package capacity

import (
	"testing"
)

const GiB = 1024 * 1024 * 1024

func flavor(memBytes, cores int64) map[string]int64 {
	return map[string]int64{ResourceMemory: memBytes, ResourceCores: cores}
}

func host(memBytes, cores int64) HostState {
	return HostState{Remaining: map[string]int64{ResourceMemory: memBytes, ResourceCores: cores}}
}

func TestFits(t *testing.T) {
	tests := []struct {
		name      string
		flavor    map[string]int64
		remaining map[string]int64
		want      bool
	}{
		{
			name:      "all resources fit",
			flavor:    flavor(4*GiB, 2),
			remaining: map[string]int64{ResourceMemory: 8 * GiB, ResourceCores: 4},
			want:      true,
		},
		{
			name:      "memory too small",
			flavor:    flavor(4*GiB, 2),
			remaining: map[string]int64{ResourceMemory: 3 * GiB, ResourceCores: 4},
			want:      false,
		},
		{
			name:      "CPU too small",
			flavor:    flavor(4*GiB, 2),
			remaining: map[string]int64{ResourceMemory: 8 * GiB, ResourceCores: 1},
			want:      false,
		},
		{
			name:      "exact fit",
			flavor:    flavor(4*GiB, 2),
			remaining: map[string]int64{ResourceMemory: 4 * GiB, ResourceCores: 2},
			want:      true,
		},
		{
			name:      "zero CPU in flavor always fits on CPU",
			flavor:    map[string]int64{ResourceMemory: 4 * GiB, ResourceCores: 0},
			remaining: map[string]int64{ResourceMemory: 4 * GiB, ResourceCores: 0},
			want:      true,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := fits(tc.flavor, tc.remaining); got != tc.want {
				t.Errorf("fits() = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestInitGroupStates(t *testing.T) {
	tests := []struct {
		name          string
		groups        []GroupInput
		hosts         map[string]HostState
		wantRemaining map[string][]string // group name → expected remaining hosts
	}{
		{
			name: "eligible hosts included",
			groups: []GroupInput{
				{Name: "g1", FlavorResources: flavor(4*GiB, 2), CandidateHosts: []string{"h1", "h2"}},
			},
			hosts: map[string]HostState{
				"h1": host(8*GiB, 4),
				"h2": host(2*GiB, 4), // too small
			},
			wantRemaining: map[string][]string{"g1": {"h1"}},
		},
		{
			name: "CPU exhausted host excluded",
			groups: []GroupInput{
				{Name: "g1", FlavorResources: flavor(4*GiB, 2), CandidateHosts: []string{"h1"}},
			},
			hosts: map[string]HostState{
				"h1": host(8*GiB, 0), // zero CPU
			},
			wantRemaining: map[string][]string{"g1": {}},
		},
		{
			name: "remaining hosts sorted for stable order",
			groups: []GroupInput{
				{Name: "g1", FlavorResources: flavor(4*GiB, 2), CandidateHosts: []string{"hb", "ha", "hc"}},
			},
			hosts: map[string]HostState{
				"ha": host(8*GiB, 4),
				"hb": host(8*GiB, 4),
				"hc": host(8*GiB, 4),
			},
			wantRemaining: map[string][]string{"g1": {"ha", "hb", "hc"}},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			states := initGroupStates(tc.groups, tc.hosts)
			if len(states) != len(tc.groups) {
				t.Fatalf("initGroupStates returned %d states, want %d (one per group)", len(states), len(tc.groups))
			}
			for _, st := range states {
				want := tc.wantRemaining[st.input.Name]
				if len(st.remaining) != len(want) {
					t.Errorf("group %s: remaining = %v, want %v", st.input.Name, st.remaining, want)
					continue
				}
				for i := range want {
					if st.remaining[i] != want[i] {
						t.Errorf("group %s: remaining[%d] = %q, want %q", st.input.Name, i, st.remaining[i], want[i])
					}
				}
			}
		})
	}
}

func TestComputeFreeResources(t *testing.T) {
	tests := []struct {
		name          string
		groups        []GroupInput
		hosts         map[string]HostState
		wantFreeMem   map[string]int64
		wantFreeCores map[string]int64 // optional; only set when exercising CPU binding
	}{
		{
			name: "full slots counted",
			groups: []GroupInput{
				{Name: "g1", FlavorResources: flavor(4*GiB, 2), CandidateHosts: []string{"h1"}},
			},
			hosts:         map[string]HostState{"h1": host(8*GiB, 4)},
			wantFreeMem:   map[string]int64{"g1": 8 * GiB},
			wantFreeCores: map[string]int64{"g1": 4},
		},
		{
			name: "sub-flavor remainder excluded",
			groups: []GroupInput{
				{Name: "g1", FlavorResources: flavor(4*GiB, 2), CandidateHosts: []string{"h1"}},
			},
			hosts:       map[string]HostState{"h1": host(6*GiB, 8)},
			wantFreeMem: map[string]int64{"g1": 4 * GiB},
		},
		{
			// Memory allows 4 slots, CPU allows 1 → 1 slot usable for both resources.
			name: "CPU is binding constraint",
			groups: []GroupInput{
				{Name: "g1", FlavorResources: flavor(4*GiB, 2), CandidateHosts: []string{"h1"}},
			},
			hosts:         map[string]HostState{"h1": host(16*GiB, 2)},
			wantFreeMem:   map[string]int64{"g1": 4 * GiB},
			wantFreeCores: map[string]int64{"g1": 2},
		},
		{
			name: "host below flavor threshold contributes nothing",
			groups: []GroupInput{
				{Name: "g1", FlavorResources: flavor(4*GiB, 2), CandidateHosts: []string{"h1"}},
			},
			hosts:       map[string]HostState{"h1": host(3*GiB, 4)},
			wantFreeMem: map[string]int64{"g1": 0},
		},
		{
			name: "two candidate hosts summed",
			groups: []GroupInput{
				{Name: "g1", FlavorResources: flavor(4*GiB, 2), CandidateHosts: []string{"h1", "h2"}},
			},
			hosts: map[string]HostState{
				"h1": host(8*GiB, 4),
				"h2": host(4*GiB, 2),
			},
			wantFreeMem: map[string]int64{"g1": 12 * GiB},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			free := computeFreeResources(tc.groups, tc.hosts)
			for group, wantMem := range tc.wantFreeMem {
				if got := free[group][ResourceMemory]; got != wantMem {
					t.Errorf("free[%s][memory] = %d, want %d", group, got, wantMem)
				}
			}
			for group, wantCores := range tc.wantFreeCores {
				if got := free[group][ResourceCores]; got != wantCores {
					t.Errorf("free[%s][cores] = %d, want %d", group, got, wantCores)
				}
			}
		})
	}
}

func TestComputeUnassigned(t *testing.T) {
	tests := []struct {
		name           string
		groups         []GroupInput
		hostRes        map[string]map[string]int64
		wantUnassigned map[string]int64
	}{
		{
			name: "candidate leftover counted",
			groups: []GroupInput{
				{Name: "g1", FlavorResources: flavor(4*GiB, 2), CandidateHosts: []string{"h1"}},
			},
			hostRes:        map[string]map[string]int64{"h1": {ResourceMemory: 2 * GiB, ResourceCores: 0}},
			wantUnassigned: map[string]int64{ResourceMemory: 2 * GiB},
		},
		{
			name: "non-candidate host excluded",
			groups: []GroupInput{
				{Name: "g1", FlavorResources: flavor(4*GiB, 2), CandidateHosts: []string{"h1"}},
			},
			hostRes: map[string]map[string]int64{
				"h1": {ResourceMemory: 0},
				"h2": {ResourceMemory: 8 * GiB}, // not a candidate
			},
			wantUnassigned: map[string]int64{ResourceMemory: 0},
		},
		{
			name:           "nothing unassigned when fully used",
			groups:         []GroupInput{{Name: "g1", FlavorResources: flavor(4*GiB, 2), CandidateHosts: []string{"h1"}}},
			hostRes:        map[string]map[string]int64{"h1": {ResourceMemory: 0}},
			wantUnassigned: map[string]int64{ResourceMemory: 0},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := computeUnassigned(tc.groups, tc.hostRes)
			for r, want := range tc.wantUnassigned {
				if got[r] != want {
					t.Errorf("unassigned[%s] = %d, want %d", r, got[r], want)
				}
			}
		})
	}
}

func TestBestHost(t *testing.T) {
	tests := []struct {
		name      string
		remaining []string
		hostRes   map[string]map[string]int64
		flavorRes map[string]int64
		want      string
	}{
		{
			name:      "lower modulo remainder preferred",
			remaining: []string{"h1", "h2"},
			hostRes: map[string]map[string]int64{
				"h1": {ResourceMemory: 5 * GiB, ResourceCores: 8},
				"h2": {ResourceMemory: 8 * GiB, ResourceCores: 4}, // 8%4=0 waste
			},
			flavorRes: flavor(4*GiB, 2),
			want:      "h2",
		},
		{
			name:      "equal waste: prefer less remaining memory",
			remaining: []string{"h1", "h2"},
			hostRes: map[string]map[string]int64{
				"h1": {ResourceMemory: 8 * GiB, ResourceCores: 4},
				"h2": {ResourceMemory: 4 * GiB, ResourceCores: 4}, // less remaining
			},
			flavorRes: flavor(4*GiB, 2),
			want:      "h2",
		},
		{
			name:      "equal memory: prefer less CPU",
			remaining: []string{"h1", "h2"},
			hostRes: map[string]map[string]int64{
				"h1": {ResourceMemory: 4 * GiB, ResourceCores: 8},
				"h2": {ResourceMemory: 4 * GiB, ResourceCores: 4}, // fewer cores
			},
			flavorRes: flavor(4*GiB, 2),
			want:      "h2",
		},
		{
			name:      "all equal: lexicographic tiebreaker",
			remaining: []string{"hb", "ha"},
			hostRes: map[string]map[string]int64{
				"ha": {ResourceMemory: 4 * GiB, ResourceCores: 2},
				"hb": {ResourceMemory: 4 * GiB, ResourceCores: 2},
			},
			flavorRes: flavor(4*GiB, 2),
			want:      "ha",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := bestHost(tc.remaining, tc.hostRes, tc.flavorRes); got != tc.want {
				t.Errorf("bestHost() = %q, want %q", got, tc.want)
			}
		})
	}
}

// TestSplitCapacity covers the full round-robin assignment algorithm end-to-end.
func TestSplitCapacity(t *testing.T) {
	tests := []struct {
		name                string
		groups              []GroupInput
		hosts               map[string]HostState
		wantAssignedMem     map[string]int64
		wantAssignedCores   map[string]int64 // optional
		wantUnassignedMem   int64
		wantUnassignedCores int64            // optional
		wantFreeMem         map[string]int64 // optional
	}{
		{
			name: "single group, two hosts",
			groups: []GroupInput{
				{Name: "hana", FlavorResources: flavor(4*GiB, 2), CandidateHosts: []string{"h1", "h2"}},
			},
			hosts: map[string]HostState{
				"h1": host(8*GiB, 4), // 2 slots
				"h2": host(4*GiB, 2), // 1 slot
			},
			wantAssignedMem:   map[string]int64{"hana": 3 * 4 * GiB},
			wantAssignedCores: map[string]int64{"hana": 3 * 2},
			wantUnassignedMem: 0,
		},
		{
			name: "disjoint groups, each with own host",
			groups: []GroupInput{
				{Name: "gp", FlavorResources: flavor(4*GiB, 2), CandidateHosts: []string{"h1"}},
				{Name: "hana", FlavorResources: flavor(8*GiB, 4), CandidateHosts: []string{"h2"}},
			},
			hosts: map[string]HostState{
				"h1": host(8*GiB, 4),
				"h2": host(8*GiB, 4),
			},
			wantAssignedMem:   map[string]int64{"gp": 8 * GiB, "hana": 8 * GiB},
			wantUnassignedMem: 0,
		},
		{
			// Both groups share the same host; round-robin gives each one slot.
			name: "overlapping groups, fair round-robin split",
			groups: []GroupInput{
				{Name: "alpha", FlavorResources: flavor(4*GiB, 2), CandidateHosts: []string{"shared"}},
				{Name: "beta", FlavorResources: flavor(4*GiB, 2), CandidateHosts: []string{"shared"}},
			},
			hosts: map[string]HostState{
				"shared": host(8*GiB, 4),
			},
			wantAssignedMem:   map[string]int64{"alpha": 4 * GiB, "beta": 4 * GiB},
			wantUnassignedMem: 0,
		},
		{
			// "constrained" has only one candidate host; fewer candidates → served first.
			name: "overlapping groups, constrained group served first",
			groups: []GroupInput{
				{Name: "constrained", FlavorResources: flavor(4*GiB, 2), CandidateHosts: []string{"shared"}},
				{Name: "free", FlavorResources: flavor(4*GiB, 2), CandidateHosts: []string{"shared", "exclusive"}},
			},
			hosts: map[string]HostState{
				"shared":    host(4*GiB, 2),
				"exclusive": host(8*GiB, 4),
			},
			wantAssignedMem:   map[string]int64{"constrained": 4 * GiB, "free": 8 * GiB},
			wantUnassignedMem: 0,
		},
		{
			// Host has memory but no CPU — flavor requires both, so host is ineligible.
			name: "CPU exhausted host dropped",
			groups: []GroupInput{
				{Name: "gp", FlavorResources: flavor(4*GiB, 2), CandidateHosts: []string{"cpu-full"}},
			},
			hosts: map[string]HostState{
				"cpu-full": host(16*GiB, 0),
			},
			wantAssignedMem:     map[string]int64{"gp": 0},
			wantUnassignedMem:   16 * GiB,
			wantUnassignedCores: 0, // CPU was zero, so nothing to strand on that axis
		},
		{
			name: "host too small for flavor",
			groups: []GroupInput{
				{Name: "hana", FlavorResources: flavor(4*GiB, 2), CandidateHosts: []string{"h1"}},
			},
			hosts: map[string]HostState{
				"h1": host(3*GiB, 4),
			},
			wantAssignedMem:   map[string]int64{"hana": 0},
			wantUnassignedMem: 3 * GiB,
		},
		{
			// Same candidate count → tiebreak on larger flavor first: hana8 goes before gp4.
			name: "larger flavor served first on candidate-count tie",
			groups: []GroupInput{
				{Name: "hana8", FlavorResources: flavor(8*GiB, 4), CandidateHosts: []string{"h1"}},
				{Name: "gp4", FlavorResources: flavor(4*GiB, 2), CandidateHosts: []string{"h1"}},
			},
			hosts: map[string]HostState{
				"h1": host(12*GiB, 8),
			},
			wantAssignedMem:   map[string]int64{"hana8": 8 * GiB, "gp4": 4 * GiB},
			wantUnassignedMem: 0,
		},
		{
			// h2 chosen first (0 waste); 1 GiB strands on h1 after its one slot.
			name: "host selection prefers lower modulo remainder",
			groups: []GroupInput{
				{Name: "gp", FlavorResources: flavor(4*GiB, 2), CandidateHosts: []string{"h1", "h2"}},
			},
			hosts: map[string]HostState{
				"h1": host(5*GiB, 8),
				"h2": host(8*GiB, 4),
			},
			wantAssignedMem:   map[string]int64{"gp": 3 * 4 * GiB},
			wantUnassignedMem: 1 * GiB,
		},
		{
			// h1: floor(6/4)*4 = 4 GiB usable; h2: below threshold → 0.
			name: "free capacity excludes sub-flavor remainder",
			groups: []GroupInput{
				{Name: "gp", FlavorResources: flavor(4*GiB, 2), CandidateHosts: []string{"h1", "h2"}},
			},
			hosts: map[string]HostState{
				"h1": host(6*GiB, 8),
				"h2": host(3*GiB, 4),
			},
			wantAssignedMem:   map[string]int64{"gp": 4 * GiB},
			wantUnassignedMem: 2*GiB + 3*GiB,
			wantFreeMem:       map[string]int64{"gp": 4 * GiB},
		},
		{
			name:              "no groups",
			groups:            nil,
			hosts:             map[string]HostState{"h1": host(8*GiB, 16)},
			wantAssignedMem:   map[string]int64{},
			wantUnassignedMem: 0,
		},
		{
			name:              "no hosts",
			groups:            []GroupInput{{Name: "hana", FlavorResources: flavor(4*GiB, 2), CandidateHosts: []string{"h1"}}},
			hosts:             map[string]HostState{},
			wantAssignedMem:   map[string]int64{"hana": 0},
			wantUnassignedMem: 0,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			free, assigned, unassigned := SplitCapacity(tc.groups, tc.hosts)

			for groupName, wantMem := range tc.wantAssignedMem {
				if got := assigned[groupName][ResourceMemory]; got != wantMem {
					t.Errorf("assigned[%s][memory] = %d, want %d", groupName, got, wantMem)
				}
			}
			for groupName, wantCores := range tc.wantAssignedCores {
				if got := assigned[groupName][ResourceCores]; got != wantCores {
					t.Errorf("assigned[%s][cores] = %d, want %d", groupName, got, wantCores)
				}
			}
			if got := unassigned[ResourceMemory]; got != tc.wantUnassignedMem {
				t.Errorf("unassigned[memory] = %d, want %d", got, tc.wantUnassignedMem)
			}
			if tc.wantUnassignedCores != 0 {
				if got := unassigned[ResourceCores]; got != tc.wantUnassignedCores {
					t.Errorf("unassigned[cores] = %d, want %d", got, tc.wantUnassignedCores)
				}
			}
			for groupName, wantMem := range tc.wantFreeMem {
				if got := free[groupName][ResourceMemory]; got != wantMem {
					t.Errorf("free[%s][memory] = %d, want %d", groupName, got, wantMem)
				}
			}
		})
	}
}

// TestSplitCapacity_SumNeverExceedsTotal is a property test: total assigned memory
// must never exceed total available memory across all hosts.
func TestSplitCapacity_SumNeverExceedsTotal(t *testing.T) {
	groups := []GroupInput{
		{Name: "g1", FlavorResources: flavor(4*GiB, 2), CandidateHosts: []string{"h1", "h2", "h3"}},
		{Name: "g2", FlavorResources: flavor(8*GiB, 4), CandidateHosts: []string{"h2", "h3"}},
		{Name: "g3", FlavorResources: flavor(4*GiB, 2), CandidateHosts: []string{"h1", "h3"}},
	}
	hosts := map[string]HostState{
		"h1": host(12*GiB, 6),
		"h2": host(16*GiB, 8),
		"h3": host(24*GiB, 12),
	}

	_, assigned, _ := SplitCapacity(groups, hosts)

	var totalInstalled, totalAssigned int64
	for _, hs := range hosts {
		totalInstalled += hs.Remaining[ResourceMemory]
	}
	for _, res := range assigned {
		totalAssigned += res[ResourceMemory]
	}
	if totalAssigned > totalInstalled {
		t.Errorf("totalAssigned (%d) > totalInstalled (%d): capacity overreported", totalAssigned, totalInstalled)
	}
}

// TestSplitCapacity_Deterministic verifies identical input always produces identical output.
func TestSplitCapacity_Deterministic(t *testing.T) {
	groups := []GroupInput{
		{Name: "c", FlavorResources: flavor(4*GiB, 2), CandidateHosts: []string{"h1", "h2"}},
		{Name: "a", FlavorResources: flavor(8*GiB, 4), CandidateHosts: []string{"h1", "h2"}},
		{Name: "b", FlavorResources: flavor(4*GiB, 2), CandidateHosts: []string{"h2"}},
	}
	hosts := map[string]HostState{
		"h1": host(16*GiB, 8),
		"h2": host(8*GiB, 4),
	}

	_, first, firstUnassigned := SplitCapacity(groups, hosts)
	for i := range 10 {
		_, got, gotUnassigned := SplitCapacity(groups, hosts)
		for _, g := range groups {
			if got[g.Name][ResourceMemory] != first[g.Name][ResourceMemory] {
				t.Errorf("run %d: assigned[%s][memory] = %d, want %d (non-deterministic)",
					i, g.Name, got[g.Name][ResourceMemory], first[g.Name][ResourceMemory])
			}
		}
		if gotUnassigned[ResourceMemory] != firstUnassigned[ResourceMemory] {
			t.Errorf("run %d: unassigned[memory] = %d, want %d (non-deterministic)",
				i, gotUnassigned[ResourceMemory], firstUnassigned[ResourceMemory])
		}
	}
}
