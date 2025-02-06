// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package conf

import "slices"

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
func (g *DependencyGraph[K]) Resolve() [][]K {
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

	// Invert the result to get the correct order.
	slices.Reverse(result)
	return result
}
