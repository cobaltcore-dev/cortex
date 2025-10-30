// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package cinder

import (
	"log/slog"
	"testing"

	"github.com/cobaltcore-dev/cortex/lib/conf"
	"github.com/cobaltcore-dev/cortex/lib/db"
	api "github.com/cobaltcore-dev/cortex/scheduling/api/delegation/cinder"
	"github.com/cobaltcore-dev/cortex/scheduling/api/v1alpha1"
	"github.com/cobaltcore-dev/cortex/scheduling/internal/lib"
	testlibDB "github.com/cobaltcore-dev/cortex/testlib/db"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestNewPipeline(t *testing.T) {
	dbEnv := testlibDB.SetupDBEnv(t)
	testDB := db.DB{DbMap: dbEnv.DbMap}
	defer testDB.Close()
	defer dbEnv.Close()

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
			name: "pipeline with unsupported step",
			steps: []v1alpha1.Step{
				{
					ObjectMeta: metav1.ObjectMeta{
						Name: "unsupported-step",
					},
					Spec: v1alpha1.StepSpec{
						Type: v1alpha1.StepTypeFilter,
						Impl: "unsupported-impl",
					},
				},
			},
			expectError: true,
		},
		{
			name: "pipeline with weigher step",
			steps: []v1alpha1.Step{
				{
					ObjectMeta: metav1.ObjectMeta{
						Name: "weigher-step",
					},
					Spec: v1alpha1.StepSpec{
						Type: v1alpha1.StepTypeWeigher,
						Impl: "test-weigher",
						Weigher: &v1alpha1.WeigherSpec{
							DisabledValidations: v1alpha1.DisabledValidationsSpec{
								SameSubjectNumberInOut: true,
							},
						},
					},
				},
			},
			expectError: true, // Will fail because test-weigher is not in supportedSteps
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			monitor := lib.PipelineMonitor{}
			pipeline, err := NewPipeline(tt.steps, testDB, monitor)

			if tt.expectError && err == nil {
				t.Error("Expected error but got none")
			}
			if !tt.expectError && err != nil {
				t.Errorf("Expected no error but got: %v", err)
			}
			if !tt.expectError && pipeline == nil {
				t.Error("Expected pipeline but got nil")
			}
		})
	}
}

func TestPipelineRun(t *testing.T) {
	dbEnv := testlibDB.SetupDBEnv(t)
	testDB := db.DB{DbMap: dbEnv.DbMap}
	defer testDB.Close()
	defer dbEnv.Close()

	monitor := lib.PipelineMonitor{}
	pipeline, err := NewPipeline([]v1alpha1.Step{}, testDB, monitor)
	if err != nil {
		t.Fatalf("Failed to create pipeline: %v", err)
	}

	tests := []struct {
		name               string
		request            api.ExternalSchedulerRequest
		expectedHostsCount int
	}{
		{
			name: "basic cinder request",
			request: api.ExternalSchedulerRequest{
				Spec: map[string]any{
					"volume_id": "test-volume",
					"size":      10,
				},
				Context: api.CinderRequestContext{
					ProjectID:       "test-project",
					UserID:          "test-user",
					RequestID:       "req-123",
					GlobalRequestID: "global-req-123",
				},
				Hosts: []api.ExternalSchedulerHost{
					{VolumeHost: "cinder-volume-1"},
					{VolumeHost: "cinder-volume-2"},
				},
				Weights:  map[string]float64{"cinder-volume-1": 1.0, "cinder-volume-2": 0.5},
				Pipeline: "test-pipeline",
			},
			expectedHostsCount: 2,
		},
		{
			name: "single host request",
			request: api.ExternalSchedulerRequest{
				Spec: map[string]any{
					"volume_id": "test-volume-single",
				},
				Context: api.CinderRequestContext{
					ProjectID: "test-project",
					UserID:    "test-user",
				},
				Hosts: []api.ExternalSchedulerHost{
					{VolumeHost: "cinder-volume-1"},
				},
				Weights:  map[string]float64{"cinder-volume-1": 2.0},
				Pipeline: "test-pipeline",
			},
			expectedHostsCount: 1,
		},
		{
			name: "no hosts request",
			request: api.ExternalSchedulerRequest{
				Spec: map[string]any{
					"volume_id": "test-volume-empty",
				},
				Context: api.CinderRequestContext{
					ProjectID: "test-project",
					UserID:    "test-user",
				},
				Hosts:    []api.ExternalSchedulerHost{},
				Weights:  map[string]float64{},
				Pipeline: "test-pipeline",
			},
			expectedHostsCount: 0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := pipeline.Run(tt.request)

			if err != nil {
				t.Errorf("Expected no error but got: %v", err)
			}

			if len(result.OrderedHosts) != tt.expectedHostsCount {
				t.Errorf("Expected %d hosts but got %d", tt.expectedHostsCount, len(result.OrderedHosts))
			}

			if tt.expectedHostsCount > 0 {
				if result.TargetHost == nil {
					t.Error("Expected target host to be set but was nil")
				} else if *result.TargetHost != result.OrderedHosts[0] {
					t.Errorf("Expected target host %s but got %s", result.OrderedHosts[0], *result.TargetHost)
				}
			} else {
				if result.TargetHost != nil {
					t.Error("Expected target host to be nil but was set")
				}
			}

			if result.RawInWeights == nil {
				t.Error("Expected raw input weights to be set")
			}

			if result.NormalizedInWeights == nil {
				t.Error("Expected normalized input weights to be set")
			}

			if result.AggregatedOutWeights == nil {
				t.Error("Expected aggregated output weights to be set")
			}
		})
	}
}

func TestCinderStepType(t *testing.T) {
	dbEnv := testlibDB.SetupDBEnv(t)
	testDB := db.DB{DbMap: dbEnv.DbMap}
	defer testDB.Close()
	defer dbEnv.Close()

	var step CinderStep = &mockCinderStep{}

	// Test initialization
	opts := conf.NewRawOpts(`{}`)
	err := step.Init(testDB, opts)
	if err != nil {
		t.Errorf("Expected no error but got: %v", err)
	}
}

// Mock implementation for testing
type mockCinderStep struct{}

func (s *mockCinderStep) Init(db db.DB, opts conf.RawOpts) error {
	return nil
}

func (s *mockCinderStep) Run(logger *slog.Logger, request api.ExternalSchedulerRequest) (*lib.StepResult, error) {
	return &lib.StepResult{
		Activations: make(map[string]float64),
	}, nil
}

func (s *mockCinderStep) GetName() string {
	return "mock-cinder-step"
}
