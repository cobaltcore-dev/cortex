// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package plugins

import "math"

type Weight = float64
type Weights = map[string]float64

// Mixin that can be embedded in a step to provide some activation function tooling.
type ActivationFunction struct{}

// Get activations that will have no effect on the host.
func (m *ActivationFunction) NoEffect() Weight { return 0 }

// Apply the activation function to the weights map.
// All hosts that are not in the activations map are removed.
func (m *ActivationFunction) Apply(in, activations Weights) Weights {
	for host, prevWeight := range in {
		// Remove hosts that are not in the weights map.
		if _, ok := activations[host]; !ok {
			delete(in, host)
		} else {
			// Apply the activation from the step.
			(in)[host] = prevWeight + math.Tanh(activations[host])
		}
	}
	return in
}
