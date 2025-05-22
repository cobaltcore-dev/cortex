// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package scheduler

import "math"

// Mixin that can be embedded in a step to provide some activation function tooling.
type ActivationFunction struct{}

// Get activations that will have no effect on the host.
func (m *ActivationFunction) NoEffect() float64 { return 0 }

// Normalize a single value using the activation function.
func (m *ActivationFunction) Norm(activation float64) float64 {
	return math.Tanh(activation)
}

// Apply the activation function to the weights map.
// All hosts that are not in the activations map are removed.
func (m *ActivationFunction) Apply(in, activations map[string]float64) map[string]float64 {
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
