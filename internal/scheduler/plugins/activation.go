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

// Clamp a value between lower and upper bounds.
func clamp(value, lowerBound, upperBound float64) float64 {
	if lowerBound > upperBound {
		lowerBound, upperBound = upperBound, lowerBound
	}
	if value < lowerBound {
		return lowerBound
	}
	if value > upperBound {
		return upperBound
	}
	return value
}

// Min-max scale a value between lower and upper bounds and apply the given activation.
// Note: the resulting value is clamped between the activation bounds.
func MinMaxScale(value, lowerBound, upperBound, activationLowerBound, activationUpperBound float64) float64 {
	// Avoid zero-division during min-max scaling.
	if lowerBound == upperBound {
		return 0
	}
	if activationLowerBound == activationUpperBound {
		return 0
	}
	normalized := (value - lowerBound) / (upperBound - lowerBound)
	activation := activationLowerBound + normalized*(activationUpperBound-activationLowerBound)
	return clamp(activation, activationLowerBound, activationUpperBound)
}
