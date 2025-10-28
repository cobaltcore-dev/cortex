// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package machines

import (
	"testing"

	"github.com/cobaltcore-dev/cortex/lib/conf"
	"github.com/cobaltcore-dev/cortex/lib/db"
	"github.com/cobaltcore-dev/cortex/scheduling/api/v1alpha1"
	"github.com/cobaltcore-dev/cortex/scheduling/internal/lib"
	testlibDB "github.com/cobaltcore-dev/cortex/testlib/db"
)

func TestNewPipeline(t *testing.T) {
	dbEnv := testlibDB.SetupDBEnv(t)
	testDB := db.DB{DbMap: dbEnv.DbMap}
	defer testDB.Close()
	defer dbEnv.Close()

	// Create a mock pipeline monitor
	monitor := lib.PipelineMonitor{}

	tests := []struct {
		name        string
		steps       []v1alpha1.Step
		expectError bool
	}{
		{
			name:        "empty pipeline",
			steps:       []v1alpha1.Step{},
			expectError: false,
		},
		{
			name: "pipeline with noop step",
			steps: []v1alpha1.Step{
				{
					Spec: v1alpha1.StepSpec{
						Impl: "noop",
						Type: v1alpha1.StepTypeFilter,
					},
				},
			},
			expectError: false,
		},
		{
			name: "pipeline with unsupported step",
			steps: []v1alpha1.Step{
				{
					Spec: v1alpha1.StepSpec{
						Impl: "unsupported-step",
						Type: v1alpha1.StepTypeFilter,
					},
				},
			},
			expectError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			pipeline, err := NewPipeline(tt.steps, testDB, monitor)

			if tt.expectError {
				if err == nil {
					t.Error("expected error but got none")
				}
				return
			}

			if err != nil {
				t.Errorf("expected no error, got: %v", err)
				return
			}

			if pipeline == nil {
				t.Error("expected pipeline to be non-nil")
			}
		})
	}
}

func TestSupportedSteps(t *testing.T) {
	expectedSteps := map[string]bool{
		"noop": true,
	}

	if len(supportedSteps) != len(expectedSteps) {
		t.Errorf("expected %d supported steps, got %d", len(expectedSteps), len(supportedSteps))
	}

	for stepName := range expectedSteps {
		stepFactory, exists := supportedSteps[stepName]
		if !exists {
			t.Errorf("expected step %q to be supported", stepName)
			continue
		}

		if stepFactory == nil {
			t.Errorf("expected step factory for %q to be non-nil", stepName)
			continue
		}

		step := stepFactory()
		if step == nil {
			t.Errorf("expected step factory for %q to return non-nil step", stepName)
		}
	}
}

func TestMachineStepType(t *testing.T) {
	// Verify that MachineStep type alias is correctly defined
	var step MachineStep = &NoopFilter{}

	// Test that it implements the Step interface methods
	name := step.GetName()
	if name == "" {
		t.Error("expected GetName() to return non-empty string")
	}

	// Test Init method exists and is callable
	dbEnv := testlibDB.SetupDBEnv(t)
	testDB := db.DB{DbMap: dbEnv.DbMap}
	defer testDB.Close()
	defer dbEnv.Close()

	opts := conf.NewRawOpts(`{}`)
	err := step.Init(testDB, opts)
	if err != nil {
		t.Errorf("expected Init() to succeed, got error: %v", err)
	}
}

func TestPipelineWrappers(t *testing.T) {
	dbEnv := testlibDB.SetupDBEnv(t)
	testDB := db.DB{DbMap: dbEnv.DbMap}
	defer testDB.Close()
	defer dbEnv.Close()

	monitor := lib.PipelineMonitor{}

	steps := []v1alpha1.Step{
		{
			Spec: v1alpha1.StepSpec{
				Impl: "noop",
				Type: v1alpha1.StepTypeFilter,
			},
		},
	}

	pipeline, err := NewPipeline(steps, testDB, monitor)
	if err != nil {
		t.Fatalf("expected no error creating pipeline, got: %v", err)
	}

	if pipeline == nil {
		t.Fatal("expected pipeline to be non-nil")
	}

	// Verify the pipeline was created with the monitoring wrapper
	// This is an indirect test since we can't easily inspect the internal structure
	// but the fact that NewPipeline succeeded with a monitor indicates the wrapper was applied
}
