// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package scheduler

import (
	"log/slog"
	"reflect"
	"testing"

	"github.com/cobaltcore-dev/cortex/internal/conf"
	"github.com/cobaltcore-dev/cortex/internal/db"
)

func TestStepValidator_GetName(t *testing.T) {
	mockStep := &mockStep[mockPipelineRequest]{
		Name: "mock-step",
	}

	validator := StepValidator[mockPipelineRequest]{
		Step: mockStep,
	}

	if got := validator.GetName(); got != "mock-step" {
		t.Errorf("GetName() = %v, want %v", got, "mock-step")
	}
}

func TestStepValidator_Init(t *testing.T) {
	mockStep := &mockStep[mockPipelineRequest]{
		InitFunc: func(db db.DB, opts conf.RawOpts) error {
			return nil
		},
	}

	testDB := db.DB{}
	mockOpts := conf.RawOpts{}

	validator := StepValidator[mockPipelineRequest]{
		Step: mockStep,
	}

	if err := validator.Init(testDB, mockOpts); err != nil {
		t.Errorf("Init() error = %v, want nil", err)
	}
}

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
		DisabledValidations: conf.SchedulerStepDisabledValidationsConfig{
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
		DisabledValidations: conf.SchedulerStepDisabledValidationsConfig{
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

	expectedError := "number of subjects changed during step execution"
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
		DisabledValidations: conf.SchedulerStepDisabledValidationsConfig{
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
	disabledValidations := conf.SchedulerStepDisabledValidationsConfig{
		SameSubjectNumberInOut: true,
	}

	validator := ValidateStep(mockStep, disabledValidations)
	if !reflect.DeepEqual(validator.DisabledValidations, disabledValidations) {
		t.Errorf("validateStep() DisabledValidations = %v, want %v", validator.DisabledValidations, disabledValidations)
	}
}
