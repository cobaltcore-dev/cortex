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
			result, err := tt.graph.Resolve()
			if err != nil {
				t.Errorf("Resolve() error = %v", err)
				return
			}
			if !reflect.DeepEqual(result, tt.expected) {
				t.Errorf("Resolve() = %v, expected %v", result, tt.expected)
			}
		})
	}
}

func TestDependencyGraph_DistinctSubgraphs(t *testing.T) {
	tests := []struct {
		name      string
		graph     DependencyGraph[string]
		condition func(string) bool
		expected  []DependencyGraph[string]
	}{
		{
			name: "SingleSubgraph",
			graph: DependencyGraph[string]{
				Dependencies: map[string][]string{
					"B": {"A"},
					"C": {"B"},
				},
				Nodes: []string{"A", "B", "C"},
			},
			condition: func(node string) bool {
				return node == "A" || node == "B" || node == "C"
			},
			expected: []DependencyGraph[string]{
				{
					Dependencies: map[string][]string{
						"B": {"A"},
						"C": {"B"},
					},
					Nodes: []string{"A", "B", "C"},
				},
			},
		},
		{
			name: "MultipleSubgraphs",
			graph: DependencyGraph[string]{
				Dependencies: map[string][]string{
					"E": {"D", "C"},
					"C": {"B"},
					"D": {"B"},
					"B": {"A"},
					"I": {"H", "G"},
					"H": {"F"},
					"G": {"F"},
				},
				Nodes: []string{"A", "B", "C", "D", "E", "F", "G", "H", "I"},
			},
			condition: func(node string) bool {
				return node == "B" || node == "D" || node == "F"
			},
			expected: []DependencyGraph[string]{
				{
					Dependencies: map[string][]string{
						"E": {"D", "C"},
						"C": {"B"},
						"D": {"B"},
					},
					Nodes: []string{"B", "C", "D", "E"},
				},
				{
					Dependencies: map[string][]string{
						"I": {"H", "G"},
						"H": {"F"},
						"G": {"F"},
					},
					Nodes: []string{"F", "G", "H", "I"},
				},
			},
		},
		{
			name: "NoMatchingNodes",
			graph: DependencyGraph[string]{
				Dependencies: map[string][]string{
					"B": {"A"},
					"C": {"B"},
				},
				Nodes: []string{"A", "B", "C"},
			},
			condition: func(node string) bool {
				return false
			},
			expected: []DependencyGraph[string]{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := tt.graph.DistinctSubgraphs(tt.condition)
			if len(result) != len(tt.expected) {
				t.Errorf("DistinctSubgraphs() = %v, expected %v", result, tt.expected)
				return
			}
			for i, subgraph := range result {
				if !reflect.DeepEqual(subgraph.Dependencies, tt.expected[i].Dependencies) ||
					!reflect.DeepEqual(subgraph.Nodes, tt.expected[i].Nodes) {
					t.Errorf("DistinctSubgraphs() = %v, expected %v", result, tt.expected)
					return
				}
			}
		})
	}
}

func TestDependencyGraph_ResolveWithCycle(t *testing.T) {
	tests := []struct {
		name  string
		graph DependencyGraph[string]
	}{
		{
			name: "EasyCycle",
			graph: DependencyGraph[string]{
				Dependencies: map[string][]string{
					"A":  {"B"},
					"B:": {"A"},
				},
				Nodes: []string{"A", "B"},
			},
		},
		{
			name: "NestedCycle",
			graph: DependencyGraph[string]{
				Dependencies: map[string][]string{
					"A":  {"B"},
					"B":  {"C"},
					"C:": {"B"},
				},
				Nodes: []string{"A", "B", "C"},
			},
		},
		{
			name: "LargeCycle",
			graph: DependencyGraph[string]{
				Dependencies: map[string][]string{
					"A": {"B"},
					"B": {"C"},
					"C": {"D"},
					"D": {"E"},
					"E": {"F"},
					"F": {"A"},
				},
				Nodes: []string{"A", "B", "C", "D", "E", "F"},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := tt.graph.Resolve(); err == nil {
				t.Errorf("Expected error, got nil")
			}
		})
	}
}
