// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package conf

import (
	"reflect"
	"testing"
)

func TestDependencyGraph_Resolve(t *testing.T) {
	tests := []struct {
		name     string
		graph    DependencyGraph[string]
		expected [][]string
	}{
		{
			name: "NoDependencies",
			graph: DependencyGraph[string]{
				Dependencies: map[string][]string{},
				Nodes:        []string{"A", "B", "C"},
			},
			expected: [][]string{{"A", "B", "C"}},
		},
		{
			name: "LinearDependencies",
			graph: DependencyGraph[string]{
				Dependencies: map[string][]string{
					"B": {"A"},
					"C": {"B"},
				},
				Nodes: []string{"A", "B", "C"},
			},
			expected: [][]string{{"A"}, {"B"}, {"C"}},
		},
		{
			name: "MultipleDependencies",
			graph: DependencyGraph[string]{
				Dependencies: map[string][]string{
					"B": {"A"},
					"C": {"A"},
					"D": {"B", "C"},
				},
				Nodes: []string{"A", "B", "C", "D"},
			},
			expected: [][]string{{"A"}, {"B", "C"}, {"D"}},
		},
		{
			name: "ComplexDependencies",
			graph: DependencyGraph[string]{
				Dependencies: map[string][]string{
					"B": {"A"},
					"C": {"A"},
					"D": {"B"},
					"E": {"B", "C"},
					"F": {"D", "E"},
				},
				Nodes: []string{"A", "B", "C", "D", "E", "F"},
			},
			expected: [][]string{{"A"}, {"B", "C"}, {"D", "E"}, {"F"}},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := tt.graph.Resolve()
			if !reflect.DeepEqual(result, tt.expected) {
				t.Errorf("Resolve() = %v, expected %v", result, tt.expected)
			}
		})
	}
}
