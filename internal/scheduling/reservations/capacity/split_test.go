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

// TestSplitCapacity covers the round-robin assignment algorithm.
func TestSplitCapacity(t *testing.T) {
	tests := []struct {
		name              string
		groups            []GroupInput
		hosts             map[string]HostState
		wantAssignedMem   map[string]int64
		wantUnassignedMem int64
		// wantFreeMem: optional check on FreeCapacity per group (only set in cases that exercise it).
		wantFreeMem map[string]int64
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
			// It wins the shared slot; "free" falls back to its exclusive host.
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
			wantAssignedMem:   map[string]int64{"gp": 0},
			wantUnassignedMem: 16 * GiB,
		},
		{
			// Host has less memory than the flavor requires → nothing assigned, all memory is unassigned.
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
			// h1: 5 GiB → 5 % 4 = 1 GiB waste. h2: 8 GiB → 8 % 4 = 0 GiB waste.
			// h2 chosen first (lower modulo remainder); 1 GiB strands on h1 after its one slot.
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
			// freeResources counts floor(remaining/flavorSize)*flavorSize per host.
			// h1 has 6 GiB: floor(6/4)*4 = 4 GiB usable. h2 has 3 GiB: below flavor → 0.
			name: "free capacity excludes sub-flavor remainder",
			groups: []GroupInput{
				{Name: "gp", FlavorResources: flavor(4*GiB, 2), CandidateHosts: []string{"h1", "h2"}},
			},
			hosts: map[string]HostState{
				"h1": host(6*GiB, 8), // 1 slot usable, 2 GiB wasted
				"h2": host(3*GiB, 4), // below flavor threshold → 0 usable
			},
			wantAssignedMem:   map[string]int64{"gp": 4 * GiB},
			wantUnassignedMem: 2*GiB + 3*GiB, // 2 GiB remainder on h1 + all of h2
			wantFreeMem:       map[string]int64{"gp": 4 * GiB},
		},
		{
			name:   "no groups",
			groups: nil,
			hosts: map[string]HostState{
				"h1": host(8*GiB, 16),
			},
			wantAssignedMem:   map[string]int64{},
			wantUnassignedMem: 0,
		},
		{
			name: "no hosts",
			groups: []GroupInput{
				{Name: "hana", FlavorResources: flavor(4*GiB, 2), CandidateHosts: []string{"h1"}},
			},
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
			if got := unassigned[ResourceMemory]; got != tc.wantUnassignedMem {
				t.Errorf("unassigned[memory] = %d, want %d", got, tc.wantUnassignedMem)
			}
			for groupName, wantMem := range tc.wantFreeMem {
				if got := free[groupName][ResourceMemory]; got != wantMem {
					t.Errorf("free[%s][memory] = %d, want %d", groupName, got, wantMem)
				}
			}
		})
	}
}

// TestSplitCapacity_SumNeverExceedsTotal is a property test: the total assigned memory
// across all groups must never exceed the total available memory across all hosts.
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
