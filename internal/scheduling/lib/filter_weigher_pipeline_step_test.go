// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package lib

import (
	"errors"
	"testing"

	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

// testStepOptions implements FilterWeigherPipelineStepOpts for testing.
type testStepOptions struct {
	ValidateError error
}

func (o testStepOptions) Validate() error {
	return o.ValidateError
}

func TestBaseFilterWeigherPipelineStep_Init(t *testing.T) {
	tests := []struct {
		name        string
		params      runtime.RawExtension
		expectError bool
	}{
		{
			name: "successful initialization with valid params",
			params: runtime.RawExtension{
				Raw: []byte(`{}`),
			},
			expectError: false,
		},
		{
			name: "successful initialization with empty params",
			params: runtime.RawExtension{
				Raw: []byte(`{}`),
			},
			expectError: false,
		},
		{
			name: "error on invalid JSON params",
			params: runtime.RawExtension{
				Raw: []byte(`{invalid json}`),
			},
			expectError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			step := &BaseFilterWeigherPipelineStep[mockFilterWeigherPipelineRequest, testStepOptions]{}
			cl := fake.NewClientBuilder().Build()

			err := step.Init(t.Context(), cl, tt.params)

			if tt.expectError && err == nil {
				t.Error("expected error but got nil")
			}
			if !tt.expectError && err != nil {
				t.Errorf("expected no error but got: %v", err)
			}
			if !tt.expectError && step.Client == nil {
				t.Error("expected client to be set but it was nil")
			}
		})
	}
}

func TestBaseFilterWeigherPipelineStep_Init_ValidationError(t *testing.T) {
	// We need a custom type with a Validate method that returns an error
	step := &BaseFilterWeigherPipelineStep[mockFilterWeigherPipelineRequest, failingValidationOptions]{}
	cl := fake.NewClientBuilder().Build()

	err := step.Init(t.Context(), cl, runtime.RawExtension{Raw: []byte(`{}`)})
	if err == nil {
		t.Error("expected error from validation but got nil")
	}
}

type failingValidationOptions struct{}

func (o failingValidationOptions) Validate() error {
	return errors.New("validation failed")
}

func TestBaseFilterWeigherPipelineStep_IncludeAllHostsFromRequest(t *testing.T) {
	tests := []struct {
		name          string
		hosts         []string
		expectedCount int
	}{
		{
			name:          "multiple hosts",
			hosts:         []string{"host1", "host2", "host3"},
			expectedCount: 3,
		},
		{
			name:          "single host",
			hosts:         []string{"host1"},
			expectedCount: 1,
		},
		{
			name:          "empty hosts",
			hosts:         []string{},
			expectedCount: 0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			step := &BaseFilterWeigherPipelineStep[mockFilterWeigherPipelineRequest, testStepOptions]{
				ActivationFunction: ActivationFunction{},
			}

			request := mockFilterWeigherPipelineRequest{
				Hosts: tt.hosts,
			}

			result := step.IncludeAllHostsFromRequest(request)

			if result == nil {
				t.Fatal("expected result but got nil")
			}
			if len(result.Activations) != tt.expectedCount {
				t.Errorf("expected %d activations, got %d", tt.expectedCount, len(result.Activations))
			}
			for _, host := range tt.hosts {
				if _, ok := result.Activations[host]; !ok {
					t.Errorf("expected host %s in activations", host)
				}
			}
			if result.Statistics == nil {
				t.Error("expected statistics to be initialized")
			}
		})
	}
}

func TestBaseFilterWeigherPipelineStep_PrepareStats(t *testing.T) {
	tests := []struct {
		name         string
		hosts        []string
		unit         string
		expectedUnit string
	}{
		{
			name:         "with hosts and unit",
			hosts:        []string{"host1", "host2", "host3"},
			unit:         "percentage",
			expectedUnit: "percentage",
		},
		{
			name:         "empty hosts",
			hosts:        []string{},
			unit:         "count",
			expectedUnit: "count",
		},
		{
			name:         "empty unit",
			hosts:        []string{"host1"},
			unit:         "",
			expectedUnit: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			step := &BaseFilterWeigherPipelineStep[mockFilterWeigherPipelineRequest, testStepOptions]{}

			request := mockFilterWeigherPipelineRequest{
				Hosts: tt.hosts,
			}

			stats := step.PrepareStats(request, tt.unit)

			if stats.Unit != tt.expectedUnit {
				t.Errorf("expected unit %s, got %s", tt.expectedUnit, stats.Unit)
			}
			if stats.Hosts == nil {
				t.Error("expected hosts map to be initialized")
			}
			// Maps don't have a cap() function, but we can verify the map is initialized
			// and works correctly by checking it's not nil (already done above)
		})
	}
}
