// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package scheduler

import (
	"reflect"
	"testing"

	"github.com/cobaltcore-dev/cortex/internal/conf"
	"github.com/cobaltcore-dev/cortex/internal/db"
	"github.com/cobaltcore-dev/cortex/internal/scheduler/api"
	testlibAPI "github.com/cobaltcore-dev/cortex/testlib/scheduler/api"
)

// MockStep is a manual mock implementation of the plugins.Step interface.
type MockStep struct {
	Name     string
	InitFunc func(db db.DB, opts conf.RawOpts) error
	RunFunc  func(request api.Request) (map[string]float64, error)
}

func (m *MockStep) GetName() string {
	return m.Name
}

func (m *MockStep) Init(db db.DB, opts conf.RawOpts) error {
	return m.InitFunc(db, opts)
}

func (m *MockStep) Run(request api.Request) (map[string]float64, error) {
	return m.RunFunc(request)
}

func TestStepValidator_GetName(t *testing.T) {
	mockStep := &MockStep{
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
	mockStep := &MockStep{
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
	mockStep := &MockStep{
		RunFunc: func(request api.Request) (map[string]float64, error) {
			return map[string]float64{
				"host1": 1.0,
				"host2": 1.0,
			}, nil
		},
	}

	request := testlibAPI.MockRequest{
		Hosts: []string{"host1", "host2"},
	}

	validator := StepValidator{
		Step: mockStep,
		DisabledValidations: conf.SchedulerStepDisabledValidationsConfig{
			SameHostNumberInOut: false,
		},
	}

	weights, err := validator.Run(&request)
	if err != nil {
		t.Errorf("Run() error = %v, want nil", err)
	}

	expectedWeights := map[string]float64{
		"host1": 1.0,
		"host2": 1.0,
	}

	if !reflect.DeepEqual(weights, expectedWeights) {
		t.Errorf("Run() weights = %v, want %v", weights, expectedWeights)
	}
}

func TestStepValidator_Run_HostNumberMismatch(t *testing.T) {
	mockStep := &MockStep{
		RunFunc: func(request api.Request) (map[string]float64, error) {
			return map[string]float64{
				"host1": 1.0,
			}, nil
		},
	}

	request := testlibAPI.MockRequest{
		Hosts: []string{"host1", "host2"},
	}

	validator := StepValidator{
		Step: mockStep,
		DisabledValidations: conf.SchedulerStepDisabledValidationsConfig{
			SameHostNumberInOut: false,
		},
	}

	weights, err := validator.Run(&request)
	if err == nil {
		t.Errorf("Run() error = nil, want error")
	}

	if weights != nil {
		t.Errorf("Run() weights = %v, want nil", weights)
	}

	expectedError := "number of hosts changed during step execution"
	if err.Error() != expectedError {
		t.Errorf("Run() error = %v, want %v", err.Error(), expectedError)
	}
}

func TestStepValidator_Run_DisabledValidation(t *testing.T) {
	mockStep := &MockStep{
		RunFunc: func(request api.Request) (map[string]float64, error) {
			return map[string]float64{
				"host1": 1.0,
			}, nil
		},
	}

	request := testlibAPI.MockRequest{
		Hosts: []string{"host1"},
	}

	validator := StepValidator{
		Step: mockStep,
		DisabledValidations: conf.SchedulerStepDisabledValidationsConfig{
			SameHostNumberInOut: true, // Validation is disabled
		},
	}

	weights, err := validator.Run(&request)
	if err != nil {
		t.Errorf("Run() error = %v, want nil", err)
	}

	expectedWeights := map[string]float64{
		"host1": 1.0,
	}

	if !reflect.DeepEqual(weights, expectedWeights) {
		t.Errorf("Run() weights = %v, want %v", weights, expectedWeights)
	}
}

func TestValidateStep(t *testing.T) {
	mockStep := &MockStep{}
	disabledValidations := conf.SchedulerStepDisabledValidationsConfig{
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
