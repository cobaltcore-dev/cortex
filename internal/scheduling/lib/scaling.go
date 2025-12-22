// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package lib

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
