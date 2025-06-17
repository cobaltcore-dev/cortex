// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package nova

import (
	"errors"
	"log/slog"
	"reflect"
	"testing"

	testlibDB "github.com/cobaltcore-dev/cortex/testlib/db"
	testlibScheduler "github.com/cobaltcore-dev/cortex/testlib/scheduler"

	"github.com/cobaltcore-dev/cortex/internal/conf"
	"github.com/cobaltcore-dev/cortex/internal/db"
	"github.com/cobaltcore-dev/cortex/internal/extractor/plugins/shared"
	"github.com/cobaltcore-dev/cortex/internal/scheduler"
	"github.com/cobaltcore-dev/cortex/internal/scheduler/nova/api"
)

func setupTestDBWithHostCapabilities(t *testing.T) db.DB {
	dbEnv := testlibDB.SetupDBEnv(t)
	testDB := db.DB{DbMap: dbEnv.DbMap}
	// Create table for host capabilities
	err := testDB.CreateTable(
		testDB.AddTable(shared.HostCapabilities{}),
	)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	// Insert mock data
	_, err = testDB.Exec(`
		INSERT INTO feature_host_capabilities (compute_host, traits, hypervisor_type)
		VALUES
			('host1', 'TRAIT_A,TRAIT_B', 'kvm'),
			('host2', 'TRAIT_B', 'xen'),
			('host3', 'TRAIT_C', 'kvm')
	`)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	return testDB
}

func TestStepScoper_Run_HostSelector_Trait(t *testing.T) {
	mockStep := &testlibScheduler.MockStep[api.ExternalSchedulerRequest]{
		Name: "mock-step",
		RunFunc: func(traceLog *slog.Logger, request api.ExternalSchedulerRequest) (*scheduler.StepResult, error) {
			return &scheduler.StepResult{
				Activations: map[string]float64{
					"host1": 1.0,
					"host2": 2.0,
					"host3": 3.0,
				},
			}, nil
		},
	}
	testDB := setupTestDBWithHostCapabilities(t)
	defer testDB.Close()

	scoper := StepScoper{
		Step: mockStep,
		Scope: conf.NovaSchedulerStepScope{
			HostSelectors: []conf.NovaSchedulerStepHostSelector{{
				Subject:   "trait",
				Infix:     "TRAIT_A",
				Operation: "intersection",
			}},
		},
		DB: testDB,
	}

	request := api.ExternalSchedulerRequest{
		Hosts: []api.ExternalSchedulerHost{
			{ComputeHost: "host1"},
			{ComputeHost: "host2"},
			{ComputeHost: "host3"},
		},
	}

	result, err := scoper.Run(slog.Default(), request)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	expected := map[string]float64{
		"host1": 1.0,
		"host2": 0.0, // not in scope
		"host3": 0.0, // not in scope
	}
	if !reflect.DeepEqual(result.Activations, expected) {
		t.Errorf("activations = %v, want %v", result.Activations, expected)
	}
}

func TestStepScoper_Run_HostSelector_HypervisorType_Difference(t *testing.T) {
	mockStep := &testlibScheduler.MockStep[api.ExternalSchedulerRequest]{
		Name: "mock-step",
		RunFunc: func(traceLog *slog.Logger, request api.ExternalSchedulerRequest) (*scheduler.StepResult, error) {
			return &scheduler.StepResult{
				Activations: map[string]float64{
					"host1": 1.0,
					"host2": 2.0,
					"host3": 3.0,
				},
			}, nil
		},
	}
	testDB := setupTestDBWithHostCapabilities(t)
	defer testDB.Close()

	scoper := StepScoper{
		Step: mockStep,
		Scope: conf.NovaSchedulerStepScope{
			HostSelectors: []conf.NovaSchedulerStepHostSelector{{
				Subject:   "hypervisortype",
				Infix:     "xen",
				Operation: "difference",
			}},
		},
		DB: testDB,
	}

	request := api.ExternalSchedulerRequest{
		Hosts: []api.ExternalSchedulerHost{
			{ComputeHost: "host1"},
			{ComputeHost: "host2"},
			{ComputeHost: "host3"},
		},
	}

	result, err := scoper.Run(slog.Default(), request)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	expected := map[string]float64{
		"host1": 1.0,
		"host2": 0.0, // removed by difference
		"host3": 3.0,
	}
	if !reflect.DeepEqual(result.Activations, expected) {
		t.Errorf("activations = %v, want %v", result.Activations, expected)
	}
}

func TestStepScoper_Run_SpecSelector_Skip(t *testing.T) {
	mockStep := &testlibScheduler.MockStep[api.ExternalSchedulerRequest]{
		Name: "mock-step",
		RunFunc: func(traceLog *slog.Logger, request api.ExternalSchedulerRequest) (*scheduler.StepResult, error) {
			return &scheduler.StepResult{
				Activations: map[string]float64{
					"host1": 1.0,
					"host2": 2.0,
				},
			}, nil
		},
	}
	testDB := setupTestDBWithHostCapabilities(t)
	defer testDB.Close()

	scoper := StepScoper{
		Step: mockStep,
		Scope: conf.NovaSchedulerStepScope{
			SpecSelectors: []conf.NovaSchedulerStepSpecSelector{{
				Subject: "flavor",
				Infix:   "special",
				Action:  "skip",
			}},
		},
		DB: testDB,
	}

	request := api.ExternalSchedulerRequest{
		Hosts: []api.ExternalSchedulerHost{
			{ComputeHost: "host1"},
			{ComputeHost: "host2"},
		},
		Spec: api.NovaObject[api.NovaSpec]{
			Data: api.NovaSpec{
				Flavor: api.NovaObject[api.NovaFlavor]{
					Data: api.NovaFlavor{
						Name: "special-flavor",
					},
				},
			},
		},
	}

	_, err := scoper.Run(slog.Default(), request)
	if !errors.Is(err, scheduler.ErrStepSkipped) {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestStepScoper_Run_NoSelectors_AllInScope(t *testing.T) {
	mockStep := &testlibScheduler.MockStep[api.ExternalSchedulerRequest]{
		Name: "mock-step",
		RunFunc: func(traceLog *slog.Logger, request api.ExternalSchedulerRequest) (*scheduler.StepResult, error) {
			return &scheduler.StepResult{
				Activations: map[string]float64{
					"host1": 1.0,
					"host2": 2.0,
				},
			}, nil
		},
	}
	testDB := setupTestDBWithHostCapabilities(t)
	defer testDB.Close()

	scoper := StepScoper{
		Step:  mockStep,
		Scope: conf.NovaSchedulerStepScope{}, // No selectors
		DB:    testDB,
	}

	request := api.ExternalSchedulerRequest{
		Hosts: []api.ExternalSchedulerHost{
			{ComputeHost: "host1"},
			{ComputeHost: "host2"},
		},
	}

	result, err := scoper.Run(slog.Default(), request)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	expected := map[string]float64{
		"host1": 1.0,
		"host2": 2.0,
	}
	if !reflect.DeepEqual(result.Activations, expected) {
		t.Errorf("activations = %v, want %v", result.Activations, expected)
	}
}
