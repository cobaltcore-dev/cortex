// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package nova

import (
	"log/slog"
	"reflect"
	"testing"

	"github.com/cobaltcore-dev/cortex/internal/conf"
	"github.com/cobaltcore-dev/cortex/internal/db"
	"github.com/cobaltcore-dev/cortex/internal/scheduler/nova/api"
	"github.com/cobaltcore-dev/cortex/internal/scheduler/nova/plugins"
	testlibAPI "github.com/cobaltcore-dev/cortex/testlib/scheduler/api"
	testlibPlugins "github.com/cobaltcore-dev/cortex/testlib/scheduler/plugins"
)

func TestStepValidator_GetName(t *testing.T) {
	mockStep := &testlibPlugins.MockStep{
		Name: "mock-step",
	}

	validator := StepValidator{
		Step: mockStep,
	}

	if got := validator.GetName(); got != "mock-step" {
		t.Errorf("GetName() = %v, want %v", got, "mock-step")
	}
}

func TestStepValidator_Init(t *testing.T) {
	mockStep := &testlibPlugins.MockStep{
		InitFunc: func(db db.DB, opts conf.RawOpts) error {
			return nil
		},
	}

	testDB := db.DB{}
	mockOpts := conf.RawOpts{}

	validator := StepValidator{
		Step: mockStep,
	}

	if err := validator.Init(testDB, mockOpts); err != nil {
		t.Errorf("Init() error = %v, want nil", err)
	}
}

func TestStepValidator_Run_ValidHosts(t *testing.T) {
	mockStep := &testlibPlugins.MockStep{
		RunFunc: func(traceLog *slog.Logger, request api.Request) (*plugins.StepResult, error) {
			return &plugins.StepResult{
				Activations: map[string]float64{
					"host1": 1.0,
					"host2": 1.0,
				},
			}, nil
		},
	}

	request := testlibAPI.MockRequest{
		Hosts: []string{"host1", "host2"},
	}

	validator := StepValidator{
		Step: mockStep,
		DisabledValidations: conf.NovaSchedulerStepDisabledValidationsConfig{
			SameHostNumberInOut: false,
		},
	}

	result, err := validator.Run(slog.Default(), &request)
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
	mockStep := &testlibPlugins.MockStep{
		RunFunc: func(traceLog *slog.Logger, request api.Request) (*plugins.StepResult, error) {
			return &plugins.StepResult{
				Activations: map[string]float64{
					"host1": 1.0,
				},
			}, nil
		},
	}

	request := testlibAPI.MockRequest{
		Hosts: []string{"host1", "host2"},
	}

	validator := StepValidator{
		Step: mockStep,
		DisabledValidations: conf.NovaSchedulerStepDisabledValidationsConfig{
			SameHostNumberInOut: false,
		},
	}

	result, err := validator.Run(slog.Default(), &request)
	if err == nil {
		t.Errorf("Run() error = nil, want error")
	}

	if result != nil {
		t.Errorf("Run() weights = %v, want nil", result.Activations)
	}

	expectedError := "number of hosts changed during step execution"
	if err.Error() != expectedError {
		t.Errorf("Run() error = %v, want %v", err.Error(), expectedError)
	}
}

func TestStepValidator_Run_DisabledValidation(t *testing.T) {
	mockStep := &testlibPlugins.MockStep{
		RunFunc: func(traceLog *slog.Logger, request api.Request) (*plugins.StepResult, error) {
			return &plugins.StepResult{
				Activations: map[string]float64{
					"host1": 1.0,
				},
			}, nil
		},
	}

	request := testlibAPI.MockRequest{
		Hosts: []string{"host1"},
	}

	validator := StepValidator{
		Step: mockStep,
		DisabledValidations: conf.NovaSchedulerStepDisabledValidationsConfig{
			SameHostNumberInOut: true, // Validation is disabled
		},
	}

	result, err := validator.Run(slog.Default(), &request)
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
	mockStep := &testlibPlugins.MockStep{}
	disabledValidations := conf.NovaSchedulerStepDisabledValidationsConfig{
		SameHostNumberInOut: true,
	}

	validator := validateStep(mockStep, disabledValidations)
	if validator.Step != mockStep {
		t.Errorf("validateStep() Step = %v, want %v", validator.Step, mockStep)
	}

	if !reflect.DeepEqual(validator.DisabledValidations, disabledValidations) {
		t.Errorf("validateStep() DisabledValidations = %v, want %v", validator.DisabledValidations, disabledValidations)
	}
}
