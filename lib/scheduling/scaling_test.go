// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package scheduling

import "testing"

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
