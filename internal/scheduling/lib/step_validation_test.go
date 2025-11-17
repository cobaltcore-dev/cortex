// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package lib

import (
	"log/slog"
	"reflect"
	"testing"

	"github.com/cobaltcore-dev/cortex/api/v1alpha1"
)

func TestStepValidator_Run_ValidHosts(t *testing.T) {
	mockStep := &mockStep[mockPipelineRequest]{
		RunFunc: func(traceLog *slog.Logger, request mockPipelineRequest) (*StepResult, error) {
			return &StepResult{
				Activations: map[string]float64{
					"host1": 1.0,
					"host2": 1.0,
				},
			}, nil
		},
	}

	request := mockPipelineRequest{
		Subjects: []string{"subject1", "subject2"},
	}

	validator := StepValidator[mockPipelineRequest]{
		Step: mockStep,
		DisabledValidations: v1alpha1.DisabledValidationsSpec{
			SameSubjectNumberInOut: false,
		},
	}

	result, err := validator.Run(slog.Default(), request)
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

func TestStepValidator_Run_HostNumberMismatch(t *testing.T) {
	mockStep := &mockStep[mockPipelineRequest]{
		RunFunc: func(traceLog *slog.Logger, request mockPipelineRequest) (*StepResult, error) {
			return &StepResult{
				Activations: map[string]float64{
					"host1": 1.0,
				},
			}, nil
		},
	}

	request := mockPipelineRequest{
		Subjects: []string{"subject1", "subject2"},
	}

	validator := StepValidator[mockPipelineRequest]{
		Step: mockStep,
		DisabledValidations: v1alpha1.DisabledValidationsSpec{
			SameSubjectNumberInOut: false,
		},
	}

	result, err := validator.Run(slog.Default(), request)
	if err == nil {
		t.Errorf("Run() error = nil, want error")
	}

	if result != nil {
		t.Errorf("Run() weights = %v, want nil", result.Activations)
	}

	expectedError := "safety: number of (deduplicated) subjects changed during step execution"
	if err.Error() != expectedError {
		t.Errorf("Run() error = %v, want %v", err.Error(), expectedError)
	}
}

func TestStepValidator_Run_DisabledValidation(t *testing.T) {
	mockStep := &mockStep[mockPipelineRequest]{
		RunFunc: func(traceLog *slog.Logger, request mockPipelineRequest) (*StepResult, error) {
			return &StepResult{
				Activations: map[string]float64{
					"host1": 1.0,
				},
			}, nil
		},
	}

	request := mockPipelineRequest{
		Subjects: []string{"subject1"},
	}

	validator := StepValidator[mockPipelineRequest]{
		Step: mockStep,
		DisabledValidations: v1alpha1.DisabledValidationsSpec{
			SameSubjectNumberInOut: true, // Validation is disabled
		},
	}

	result, err := validator.Run(slog.Default(), request)
	if err != nil {
		t.Errorf("Run() error = %v, want nil", err)
	}

	expectedWeights := map[string]float64{
		"host1": 1.0,
	}

	if !reflect.DeepEqual(result.Activations, expectedWeights) {
		t.Errorf("Run() weights = %v, want %v", result.Activations, expectedWeights)
	}
}

func TestValidateStep(t *testing.T) {
	mockStep := &mockStep[mockPipelineRequest]{}
	disabledValidations := v1alpha1.DisabledValidationsSpec{
		SameSubjectNumberInOut: true,
	}

	validator := validateStep(mockStep, disabledValidations)
	if !reflect.DeepEqual(validator.DisabledValidations, disabledValidations) {
		t.Errorf("validateStep() DisabledValidations = %v, want %v", validator.DisabledValidations, disabledValidations)
	}
}
