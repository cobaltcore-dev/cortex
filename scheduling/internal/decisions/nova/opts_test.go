// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package nova

import (
	"testing"
)

func TestNovaSchedulerStepHostCapabilities_IsUndefined(t *testing.T) {
	tests := []struct {
		name     string
		caps     NovaSchedulerStepHostCapabilities
		expected bool
	}{
		{
			name:     "empty capabilities should be undefined",
			caps:     NovaSchedulerStepHostCapabilities{},
			expected: true,
		},
		{
			name: "capabilities with AnyOfTraitInfixes should not be undefined",
			caps: NovaSchedulerStepHostCapabilities{
				AnyOfTraitInfixes: []string{"TRAIT_A"},
			},
			expected: false,
		},
		{
			name: "capabilities with AnyOfHypervisorTypeInfixes should not be undefined",
			caps: NovaSchedulerStepHostCapabilities{
				AnyOfHypervisorTypeInfixes: []string{"kvm"},
			},
			expected: false,
		},
		{
			name: "capabilities with AllOfTraitInfixes should not be undefined",
			caps: NovaSchedulerStepHostCapabilities{
				AllOfTraitInfixes: []string{"TRAIT_B"},
			},
			expected: false,
		},
		{
			name: "capabilities with InvertSelection only should be undefined",
			caps: NovaSchedulerStepHostCapabilities{
				InvertSelection: true,
			},
			expected: true,
		},
		{
			name: "capabilities with multiple criteria should not be undefined",
			caps: NovaSchedulerStepHostCapabilities{
				AnyOfTraitInfixes:          []string{"TRAIT_A"},
				AnyOfHypervisorTypeInfixes: []string{"kvm"},
				AllOfTraitInfixes:          []string{"TRAIT_B"},
				InvertSelection:            true,
			},
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := tt.caps.IsUndefined()
			if result != tt.expected {
				t.Errorf("IsUndefined() = %v, want %v", result, tt.expected)
			}
		})
	}
}

func TestNovaSchedulerStepSpecScope_IsUndefined(t *testing.T) {
	tests := []struct {
		name     string
		scope    NovaSchedulerStepSpecScope
		expected bool
	}{
		{
			name:     "empty scope should be undefined",
			scope:    NovaSchedulerStepSpecScope{},
			expected: true,
		},
		{
			name: "scope with AllOfFlavorNameInfixes should not be undefined",
			scope: NovaSchedulerStepSpecScope{
				AllOfFlavorNameInfixes: []string{"large"},
			},
			expected: false,
		},
		{
			name: "scope with multiple flavor name infixes should not be undefined",
			scope: NovaSchedulerStepSpecScope{
				AllOfFlavorNameInfixes: []string{"large", "memory"},
			},
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := tt.scope.IsUndefined()
			if result != tt.expected {
				t.Errorf("IsUndefined() = %v, want %v", result, tt.expected)
			}
		})
	}
}
