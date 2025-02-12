// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package plugins

import (
	"math"
	"testing"
)

func TestActivationFunction_NoEffect(t *testing.T) {
	af := ActivationFunction{}
	expected := Weight(0)
	if af.NoEffect() != expected {
		t.Errorf("expected %v, got %v", expected, af.NoEffect())
	}
}

func TestActivationFunction_Apply(t *testing.T) {
	af := ActivationFunction{}

	tests := []struct {
		name        string
		in          Weights
		activations Weights
		expected    Weights
	}{
		{
			name: "all hosts in activations",
			in: Weights{
				"host1": 1.0,
				"host2": 2.0,
			},
			activations: Weights{
				"host1": 0.5,
				"host2": -0.5,
			},
			expected: Weights{
				"host1": 1.0 + math.Tanh(0.5),
				"host2": 2.0 + math.Tanh(-0.5),
			},
		},
		{
			name: "some hosts not in activations",
			in: Weights{
				"host1": 1.0,
				"host2": 2.0,
				"host3": 3.0,
			},
			activations: Weights{
				"host1": 0.5,
			},
			expected: Weights{
				"host1": 1.0 + math.Tanh(0.5),
			},
		},
		{
			name: "no hosts in activations",
			in: Weights{
				"host1": 1.0,
				"host2": 2.0,
			},
			activations: Weights{},
			expected:    Weights{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := af.Apply(tt.in, tt.activations)
			if len(result) != len(tt.expected) {
				t.Fatalf("expected %d hosts, got %d", len(tt.expected), len(result))
			}
			for host, weight := range tt.expected {
				if result[host] != weight {
					t.Errorf("expected weight for host %s to be %v, got %v", host, weight, result[host])
				}
			}
		})
	}
}
