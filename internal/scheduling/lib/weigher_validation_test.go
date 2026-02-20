// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package lib

import (
	"context"
	"errors"
	"log/slog"
	"reflect"
	"testing"

	"github.com/cobaltcore-dev/cortex/api/v1alpha1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func TestValidateWeigher(t *testing.T) {
	weigher := &mockWeigher[mockFilterWeigherPipelineRequest]{}
	validator := validateWeigher(weigher)

	if validator == nil {
		t.Fatal("expected validator but got nil")
	}
	if validator.Weigher != weigher {
		t.Error("expected weigher to be set in validator")
	}
}

func TestWeigherValidator_Init(t *testing.T) {
	tests := []struct {
		name        string
		weigherSpec v1alpha1.WeigherSpec
		initError   error
		expectError bool
	}{
		{
			name: "successful initialization",
			weigherSpec: v1alpha1.WeigherSpec{
				Name: "test-weigher",
				Params: runtime.RawExtension{
					Raw: []byte(`{}`),
				},
			},
			initError:   nil,
			expectError: false,
		},
		{
			name: "initialization error",
			weigherSpec: v1alpha1.WeigherSpec{
				Name: "test-weigher",
				Params: runtime.RawExtension{
					Raw: []byte(`{}`),
				},
			},
			initError:   errors.New("init error"),
			expectError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			weigher := &mockWeigher[mockFilterWeigherPipelineRequest]{
				InitFunc: func(_ context.Context, _ client.Client, _ v1alpha1.WeigherSpec) error {
					return tt.initError
				},
			}
			validator := validateWeigher(weigher)
			cl := fake.NewClientBuilder().Build()

			err := validator.Init(t.Context(), cl, tt.weigherSpec)

			if tt.expectError && err == nil {
				t.Error("expected error but got nil")
			}
			if !tt.expectError && err != nil {
				t.Errorf("expected no error but got: %v", err)
			}
		})
	}
}

func TestWeigherValidator_Run_ValidHosts(t *testing.T) {
	mockStep := &mockWeigher[mockFilterWeigherPipelineRequest]{
		RunFunc: func(_ context.Context, traceLog *slog.Logger, request mockFilterWeigherPipelineRequest) (*FilterWeigherPipelineStepResult, error) {
			return &FilterWeigherPipelineStepResult{
				Activations: map[string]float64{
					"host1": 1.0,
					"host2": 1.0,
				},
			}, nil
		},
	}

	request := mockFilterWeigherPipelineRequest{
		Hosts: []string{"host1", "host2"},
	}

	validator := WeigherValidator[mockFilterWeigherPipelineRequest]{
		Weigher: mockStep,
	}

	result, err := validator.Run(t.Context(), slog.Default(), request)
	if err != nil {
		t.Errorf("Run() error = %v, want nil", err)
	}

	expectedWeights := map[string]float64{
		"host1": 1.0,
		"host2": 1.0,
	}

	if !reflect.DeepEqual(result.Activations, expectedWeights) {
		t.Errorf("Run() weights = %v, want %v", result.Activations, expectedWeights)
	}
}

func TestWeigherValidator_Run_HostNumberMismatch(t *testing.T) {
	mockStep := &mockWeigher[mockFilterWeigherPipelineRequest]{
		RunFunc: func(_ context.Context, traceLog *slog.Logger, request mockFilterWeigherPipelineRequest) (*FilterWeigherPipelineStepResult, error) {
			return &FilterWeigherPipelineStepResult{
				Activations: map[string]float64{
					"host1": 1.0,
				},
			}, nil
		},
	}

	request := mockFilterWeigherPipelineRequest{
		Hosts: []string{"host1", "host2"},
	}

	validator := WeigherValidator[mockFilterWeigherPipelineRequest]{
		Weigher: mockStep,
	}

	result, err := validator.Run(t.Context(), slog.Default(), request)
	if err == nil {
		t.Errorf("Run() error = nil, want error")
	}

	if result != nil {
		t.Errorf("Run() weights = %v, want nil", result.Activations)
	}

	expectedError := "safety: number of (deduplicated) hosts changed during step execution"
	if err.Error() != expectedError {
		t.Errorf("Run() error = %v, want %v", err.Error(), expectedError)
	}
}
