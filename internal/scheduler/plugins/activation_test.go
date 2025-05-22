// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package plugins

import (
	"math"
	"testing"
)

func TestActivationFunction_NoEffect(t *testing.T) {
	af := ActivationFunction{}
	expected := 0.0
	if af.NoEffect() != expected {
		t.Errorf("expected %v, got %v", expected, af.NoEffect())
	}
}

func TestActivationFunction_Apply(t *testing.T) {
	af := ActivationFunction{}

	tests := []struct {
		name        string
		in          map[string]float64
		activations map[string]float64
		expected    map[string]float64
	}{
		{
			name: "all hosts in activations",
			in: map[string]float64{
				"host1": 1.0,
				"host2": 2.0,
			},
			activations: map[string]float64{
				"host1": 0.5,
				"host2": -0.5,
			},
			expected: map[string]float64{
				"host1": 1.0 + math.Tanh(0.5),
				"host2": 2.0 + math.Tanh(-0.5),
			},
		},
		{
			name: "some hosts not in activations",
			in: map[string]float64{
				"host1": 1.0,
				"host2": 2.0,
				"host3": 3.0,
			},
			activations: map[string]float64{
				"host1": 0.5,
			},
			expected: map[string]float64{
				"host1": 1.0 + math.Tanh(0.5),
			},
		},
		{
			name: "no hosts in activations",
			in: map[string]float64{
				"host1": 1.0,
				"host2": 2.0,
			},
			activations: map[string]float64{},
			expected:    map[string]float64{},
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

func TestClamp(t *testing.T) {
	tests := []struct {
		value, lowerBound, upperBound, expected float64
	}{
		{5, 0, 10, 5},
		{15, 0, 10, 10},
		{-5, 0, 10, 0},
		{5, 10, 0, 5},   // bounds are swapped
		{15, 10, 0, 10}, // bounds are swapped
		{-5, 10, 0, 0},  // bounds are swapped
	}

	for _, test := range tests {
		result := clamp(test.value, test.lowerBound, test.upperBound)
		if result != test.expected {
			t.Errorf("clamp(%v, %v, %v) = %v; want %v", test.value, test.lowerBound, test.upperBound, result, test.expected)
		}
	}
}

func TestMinMaxScale(t *testing.T) {
	tests := []struct {
		value, lowerBound, upperBound, activationLowerBound, activationUpperBound, expected float64
	}{
		{5, 0, 10, 0, 1, 0.5},
		{15, 0, 10, 0, 1, 1},
		{-5, 0, 10, 0, 1, 0},
		{5, 0, 10, 1, 2, 1.5},
		{5, 0, 0, 0, 1, 0},  // avoid zero-division
		{5, 0, 10, 1, 1, 0}, // avoid zero-division
	}

	for _, test := range tests {
		result := MinMaxScale(test.value, test.lowerBound, test.upperBound, test.activationLowerBound, test.activationUpperBound)
		if result != test.expected {
			t.Errorf("MinMaxScale(%v, %v, %v, %v, %v) = %v; want %v", test.value, test.lowerBound, test.upperBound, test.activationLowerBound, test.activationUpperBound, result, test.expected)
		}
	}
}
