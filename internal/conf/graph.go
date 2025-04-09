// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package conf

import (
	"fmt"
	"slices"
)

// Dependency graph.
type DependencyGraph[K comparable] struct {
	// Dependencies between the individual steps.
	Dependencies map[K][]K
	// List of individual steps.
	Nodes []K
}

// Resolve the dependency graph into an execution order.
// This function returns a list of steps which may contain one or
// multiple steps that can be run in parallel.
// It starts with the steps that have no dependencies and then
// recursively resolves the dependencies of the steps that depend on them.
func (g *DependencyGraph[K]) Resolve() ([][]K, error) {
	inDegree := make(map[K]int)
	for _, deps := range g.Dependencies {
		for _, dep := range deps {
			inDegree[dep]++
		}
	}

	var zeroInDegree []K
	for _, node := range g.Nodes {
		if inDegree[node] == 0 {
			zeroInDegree = append(zeroInDegree, node)
		}
	}

	var result [][]K
	for len(zeroInDegree) > 0 {
		var nextZeroInDegree []K
		result = append(result, zeroInDegree)
		for _, node := range zeroInDegree {
			for _, dep := range g.Dependencies[node] {
				inDegree[dep]--
				if inDegree[dep] == 0 {
					nextZeroInDegree = append(nextZeroInDegree, dep)
				}
			}
		}
		zeroInDegree = nextZeroInDegree
	}

	// If not all nodes could be resolved, there is a cycle in the graph.
	for _, node := range g.Nodes {
		if inDegree[node] > 0 {
			return nil, fmt.Errorf("cycle detected in dependency graph: %v", node)
		}
	}

	// Invert the result to get the correct order.
	slices.Reverse(result)
	return result, nil
}

// Get distinct subgraphs from the dependency graph where a condition is met.
//
// Example: consider the following graph:
//   - A -> B (condition: true) -> C, D (condition: true) -> E
//   - F (condition: true) -> G, H -> I
//
// The result would be two subgraphs: B -> C, D -> E and F -> G, H -> I.
// The subgraph after node D is part of B's subgraph, therefore we don't get
// three results.
func (g *DependencyGraph[K]) DistinctSubgraphs(condition func(K) bool) []DependencyGraph[K] {
	// Build the children mapping: for each dependency edge:
	// for node, and each prereq in Dependencies[node],
	// add an edge prereq -> node.
	children := make(map[K][]K)
	for node, deps := range g.Dependencies {
		for _, prereq := range deps {
			children[prereq] = append(children[prereq], node)
		}
	}

	// visited records nodes that have already been assigned to a subgraph.
	visited := make(map[K]bool)
	var distinct []DependencyGraph[K]

	// Iterate over all nodes in the original order.
	for _, node := range g.Nodes {
		if !condition(node) {
			continue
		}
		if visited[node] {
			// This condition node is already part of some earlier subgraph.
			continue
		}

		// Start a BFS from the current condition node over the children mapping
		// to collect all nodes transitively reachable (including this node).
		subNodesSet := make(map[K]bool)
		queue := []K{node}
		subNodesSet[node] = true

		for len(queue) > 0 {
			curr := queue[0]
			queue = queue[1:]
			for _, child := range children[curr] {
				if !subNodesSet[child] {
					subNodesSet[child] = true
					queue = append(queue, child)
				}
			}
		}

		// Build subNodes by filtering g.Nodes according to subNodesSet to
		// respect the original ordering.
		var subNodes []K
		for _, n := range g.Nodes {
			if subNodesSet[n] {
				subNodes = append(subNodes, n)
			}
		}

		// Build the dependencies for the subgraph.
		subDeps := make(map[K][]K)
		for _, n := range subNodes {
			if deps, ok := g.Dependencies[n]; ok {
				for _, prereq := range deps {
					if subNodesSet[prereq] {
						subDeps[n] = append(subDeps[n], prereq)
					}
				}
			}
		}

		// Create the subgraph and add it to distinct.
		subGraph := DependencyGraph[K]{
			Dependencies: subDeps,
			Nodes:        subNodes,
		}
		distinct = append(distinct, subGraph)

		// Mark all nodes in this subgraph as visited.
		for n := range subNodesSet {
			visited[n] = true
		}
	}
	return distinct
}
