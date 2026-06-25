// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package capacity

import "sort"

// Resource keys used for capacity splitting. Match CommittedResourceType constants.
const (
	ResourceMemory = "memory"
	ResourceCores  = "cores"
)

type GroupInput struct {
	Name            string
	FlavorResources map[string]int64
	CandidateHosts  []string
}

type HostState struct {
	Remaining map[string]int64
}

type groupState struct {
	input         GroupInput
	remaining     []string
	assignedCount int64
}

func fits(flavorRes, hostRemaining map[string]int64) bool {
	for r, needed := range flavorRes {
		if hostRemaining[r] < needed {
			return false
		}
	}
	return true
}

// sortGroups re-orders states in-place by the round-robin priority:
//  1. ASC number of remaining candidate hosts (fewest candidates first)
//  2. DESC flavor memory (larger flavor first)
//  3. DESC flavor cores
//  4. ASC group name (stable tiebreaker)
func sortGroups(states []groupState) {
	sort.SliceStable(states, func(i, j int) bool {
		a, b := states[i], states[j]
		if len(a.remaining) != len(b.remaining) {
			return len(a.remaining) < len(b.remaining)
		}
		if a.input.FlavorResources[ResourceMemory] != b.input.FlavorResources[ResourceMemory] {
			return a.input.FlavorResources[ResourceMemory] > b.input.FlavorResources[ResourceMemory]
		}
		if a.input.FlavorResources[ResourceCores] != b.input.FlavorResources[ResourceCores] {
			return a.input.FlavorResources[ResourceCores] > b.input.FlavorResources[ResourceCores]
		}
		return a.input.Name < b.input.Name
	})
}

// bestHost returns the index within remaining of the host to consume next.
// Sort order: least (remaining_mem % flavor_mem), then least remaining memory, then least CPU, then host name.
func bestHost(remaining []string, hostRes map[string]map[string]int64, flavorRes map[string]int64) int {
	flavorMem := flavorRes[ResourceMemory]
	best := 0
	for i, h := range remaining[1:] {
		idx := i + 1
		bh := remaining[best]
		hMem := hostRes[h][ResourceMemory]
		bMem := hostRes[bh][ResourceMemory]

		var hWaste, bWaste int64
		if flavorMem > 0 {
			hWaste = hMem % flavorMem
			bWaste = bMem % flavorMem
		}
		if hWaste != bWaste {
			if hWaste < bWaste {
				best = idx
			}
			continue
		}
		if hMem != bMem {
			if hMem < bMem {
				best = idx
			}
			continue
		}
		hCPU := hostRes[h][ResourceCores]
		bCPU := hostRes[bh][ResourceCores]
		if hCPU < bCPU || (hCPU == bCPU && h < bh) {
			best = idx
		}
	}
	return best
}

// SplitCapacity runs the round-robin capacity assignment algorithm.
//
// For each AZ it assigns resources (in raw units — bytes for memory, count for cores)
// to flavor groups in a fair, deterministic way such that no host is over-committed.
// Groups sharing hypervisors are served round-robin so no group monopolises shared hosts.
//
// Returns:
//   - freeResources[groupName][resource]: sum of remaining resources across all candidate
//     hosts for each group before the split. May overlap across groups sharing hosts.
//   - exclusiveResources[groupName][resource]: fairly attributed share after the split;
//     sum across groups never exceeds actual installed capacity.
//   - unassigned[resource]: resources on candidate hosts not claimed by any group due to
//     fragmentation (for operator log visibility).
//
// The caller divides exclusiveResources[group][ResourceMemory] by the group's flavor memory
// to obtain the slot count meaningful to that group.
func SplitCapacity(groups []GroupInput, hosts map[string]HostState) (freeResources, exclusiveResources map[string]map[string]int64, unassigned map[string]int64) {
	states := make([]groupState, len(groups))
	for i, g := range groups {
		remaining := make([]string, 0, len(g.CandidateHosts))
		for _, h := range g.CandidateHosts {
			if hs, ok := hosts[h]; ok && fits(g.FlavorResources, hs.Remaining) {
				remaining = append(remaining, h)
			}
		}
		sort.Strings(remaining) // stable initial order
		states[i] = groupState{input: g, remaining: remaining}
	}

	// freeResources: usable capacity per group — floor(remaining/flavorSize)*flavorSize per host.
	freeResources = make(map[string]map[string]int64, len(groups))
	for _, g := range groups {
		res := make(map[string]int64)
		flavorMem := g.FlavorResources[ResourceMemory]
		flavorCPU := g.FlavorResources[ResourceCores]
		for _, h := range g.CandidateHosts {
			hs, ok := hosts[h]
			if !ok {
				continue
			}
			if flavorMem > 0 {
				slots := hs.Remaining[ResourceMemory] / flavorMem
				res[ResourceMemory] += slots * flavorMem
			}
			if flavorCPU > 0 {
				slots := hs.Remaining[ResourceCores] / flavorCPU
				res[ResourceCores] += slots * flavorCPU
			}
		}
		freeResources[g.Name] = res
	}

	// Copy host resources so the caller's map is not mutated.
	hostRes := make(map[string]map[string]int64, len(hosts))
	for name, hs := range hosts {
		res := make(map[string]int64, len(hs.Remaining))
		for r, v := range hs.Remaining {
			res[r] = v
		}
		hostRes[name] = res
	}

	for {
		// Each round: serve groups in priority order, one flavor-sized allocation each.
		sortGroups(states)

		progress := false
		// Grant one allocation to each group that still has eligible candidates.
		for i := range states {
			g := &states[i]
			if len(g.remaining) == 0 {
				continue
			}

			chosen := g.remaining[bestHost(g.remaining, hostRes, g.input.FlavorResources)]
			for r, amount := range g.input.FlavorResources {
				hostRes[chosen][r] -= amount
			}
			g.assignedCount++
			progress = true

			// Drop hosts that can no longer fit their group's flavor after this allocation.
			for j := range states {
				flavorRes := states[j].input.FlavorResources
				filtered := make([]string, 0, len(states[j].remaining))
				for _, h := range states[j].remaining {
					if fits(flavorRes, hostRes[h]) {
						filtered = append(filtered, h)
					}
				}
				states[j].remaining = filtered
			}
		}

		if !progress {
			break
		}
	}

	// Unassigned: remaining resources on candidate hosts after the split.
	// Non-candidate hosts are excluded — their leftover is not fragmentation.
	candidateSet := make(map[string]struct{})
	for _, g := range groups {
		for _, h := range g.CandidateHosts {
			candidateSet[h] = struct{}{}
		}
	}
	unassigned = make(map[string]int64)
	for h, res := range hostRes {
		if _, isCandidate := candidateSet[h]; !isCandidate {
			continue
		}
		for r, remaining := range res {
			if remaining > 0 {
				unassigned[r] += remaining
			}
		}
	}

	exclusiveResources = make(map[string]map[string]int64, len(states))
	for _, g := range states {
		resources := make(map[string]int64, len(g.input.FlavorResources))
		for r, amount := range g.input.FlavorResources {
			resources[r] = g.assignedCount * amount
		}
		exclusiveResources[g.input.Name] = resources
	}
	return freeResources, exclusiveResources, unassigned
}
