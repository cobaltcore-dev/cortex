// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package plugins

import (
	"math"
	"testing"
)

func TestScaleNovaValues(t *testing.T) {
	state := &State{
		Weights: map[string]float64{
			"host1": 0.5,
			"host2": -0.5,
			"host3": 99.0,
			"host4": -99.0,
		},
	}

	state.ScaleNovaValues()

	// Check that all values are between -1 and 1
	for _, weight := range state.Weights {
		if weight < -1 || weight > 1 {
			t.Errorf("expected weight to be between -1 and 1, got %f", weight)
		}
	}
}

func TestVote(t *testing.T) {
	state := &State{
		Weights: map[string]float64{
			"host1": 0.5,
			"host2": -0.5,
		},
	}

	state.Vote("host1", 0.5)
	state.Vote("host2", -0.5)
	state.Vote("host3", 1.0) // unknown host

	expectedWeights := map[string]float64{
		"host1": 0.5 + math.Tanh(0.5),
		"host2": -0.5 + math.Tanh(-0.5),
	}

	for host, expectedWeight := range expectedWeights {
		if state.Weights[host] != expectedWeight {
			t.Errorf("expected weight for %s to be %f, got %f", host, expectedWeight, state.Weights[host])
		}
	}

	if _, ok := state.Weights["host3"]; ok {
		t.Errorf("expected host3 to not be in weights map")
	}
}
