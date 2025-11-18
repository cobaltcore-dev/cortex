// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package manila

import (
	"context"
	"encoding/json"
	"testing"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	api "github.com/cobaltcore-dev/cortex/api/delegation/manila"
	"github.com/cobaltcore-dev/cortex/api/v1alpha1"

	"github.com/cobaltcore-dev/cortex/internal/scheduling/lib"
	"github.com/cobaltcore-dev/cortex/pkg/conf"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestDecisionPipelineController_Reconcile(t *testing.T) {
	scheme := runtime.NewScheme()
	if err := v1alpha1.AddToScheme(scheme); err != nil {
		t.Fatalf("Failed to add v1alpha1 scheme: %v", err)
	}

	manilaRequest := api.ExternalSchedulerRequest{
		Spec: map[string]any{
			"share_id": "test-share-id",
			"size":     10,
		},
		Context: api.ManilaRequestContext{
			ProjectID:       "test-project",
			UserID:          "test-user",
			RequestID:       "req-123",
			GlobalRequestID: "global-req-123",
		},
		Hosts: []api.ExternalSchedulerHost{
			{ShareHost: "manila-share-1@backend1"},
			{ShareHost: "manila-share-2@backend2"},
		},
		Weights:  map[string]float64{"manila-share-1@backend1": 1.0, "manila-share-2@backend2": 0.5},
		Pipeline: "test-pipeline",
	}

	manilaRaw, err := json.Marshal(manilaRequest)
	if err != nil {
		t.Fatalf("Failed to marshal manila request: %v", err)
	}

	tests := []struct {
		name           string
		decision       *v1alpha1.Decision
		pipeline       *v1alpha1.Pipeline
		expectError    bool
		expectResult   bool
		expectDuration bool
	}{
		{
			name: "successful manila decision processing",
			decision: &v1alpha1.Decision{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-decision",
					Namespace: "default",
				},
				Spec: v1alpha1.DecisionSpec{
					Type:     v1alpha1.DecisionTypeManilaShare,
					Operator: "test-operator",
					PipelineRef: corev1.ObjectReference{
						Name: "test-pipeline",
					},
					ManilaRaw: &runtime.RawExtension{
						Raw: manilaRaw,
					},
				},
			},
			pipeline: &v1alpha1.Pipeline{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-pipeline",
				},
				Spec: v1alpha1.PipelineSpec{
					Type:     v1alpha1.PipelineTypeFilterWeigher,
					Operator: "test-operator",
					Steps:    []v1alpha1.StepInPipeline{},
				},
			},
			expectError:    false,
			expectResult:   true,
			expectDuration: true,
		},
		{
			name: "decision without manilaRaw spec",
			decision: &v1alpha1.Decision{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-decision-no-raw",
					Namespace: "default",
				},
				Spec: v1alpha1.DecisionSpec{
					Type:     v1alpha1.DecisionTypeManilaShare,
					Operator: "test-operator",
					PipelineRef: corev1.ObjectReference{
						Name: "test-pipeline",
					},
					ManilaRaw: nil,
				},
			},
			pipeline: &v1alpha1.Pipeline{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-pipeline",
				},
				Spec: v1alpha1.PipelineSpec{
					Type:     v1alpha1.PipelineTypeFilterWeigher,
					Operator: "test-operator",
					Steps:    []v1alpha1.StepInPipeline{},
				},
			},
			expectError:    true,
			expectResult:   false,
			expectDuration: false,
		},
		{
			name: "pipeline not found",
			decision: &v1alpha1.Decision{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-decision-no-pipeline",
					Namespace: "default",
				},
				Spec: v1alpha1.DecisionSpec{
					Type:     v1alpha1.DecisionTypeManilaShare,
					Operator: "test-operator",
					PipelineRef: corev1.ObjectReference{
						Name: "nonexistent-pipeline",
					},
					ManilaRaw: &runtime.RawExtension{
						Raw: manilaRaw,
					},
				},
			},
			pipeline:       nil,
			expectError:    true,
			expectResult:   false,
			expectDuration: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			objects := []client.Object{tt.decision}
			if tt.pipeline != nil {
				objects = append(objects, tt.pipeline)
			}

			client := fake.NewClientBuilder().
				WithScheme(scheme).
				WithObjects(objects...).
				WithStatusSubresource(&v1alpha1.Decision{}).
				Build()

			controller := &DecisionPipelineController{
				BasePipelineController: lib.BasePipelineController[lib.Pipeline[api.ExternalSchedulerRequest]]{
					Client:    client,
					Pipelines: make(map[string]lib.Pipeline[api.ExternalSchedulerRequest]),
				},
				Monitor: lib.PipelineMonitor{},
				Conf: conf.Config{
					Operator: "test-operator",
				},
			}

			if tt.pipeline != nil {
				pipeline, err := controller.InitPipeline(t.Context(), tt.pipeline.Name, []v1alpha1.Step{})
				if err != nil {
					t.Fatalf("Failed to init pipeline: %v", err)
				}
				controller.Pipelines[tt.pipeline.Name] = pipeline
			}

			req := ctrl.Request{
				NamespacedName: types.NamespacedName{
					Name:      tt.decision.Name,
					Namespace: tt.decision.Namespace,
				},
			}

			result, err := controller.Reconcile(context.Background(), req)

			if tt.expectError && err == nil {
				t.Error("Expected error but got none")
			}
			if !tt.expectError && err != nil {
				t.Errorf("Expected no error but got: %v", err)
			}

			if result.RequeueAfter > 0 {
				t.Error("Expected no requeue")
			}

			var updatedDecision v1alpha1.Decision
			if err := client.Get(context.Background(), req.NamespacedName, &updatedDecision); err != nil {
				t.Fatalf("Failed to get updated decision: %v", err)
			}

			if tt.expectResult && updatedDecision.Status.Result == nil {
				t.Error("Expected result to be set but was nil")
			}
			if !tt.expectResult && updatedDecision.Status.Result != nil {
				t.Error("Expected result to be nil but was set")
			}

			if tt.expectDuration && updatedDecision.Status.Took.Duration == 0 {
				t.Error("Expected duration to be set but was zero")
			}
			if !tt.expectDuration && updatedDecision.Status.Took.Duration != 0 {
				t.Error("Expected duration to be zero but was set")
			}
		})
	}
}

func TestDecisionPipelineController_ProcessNewDecisionFromAPI(t *testing.T) {
	scheme := runtime.NewScheme()
	if err := v1alpha1.AddToScheme(scheme); err != nil {
		t.Fatalf("Failed to add v1alpha1 scheme: %v", err)
	}

	manilaRequest := api.ExternalSchedulerRequest{
		Spec: map[string]any{
			"share_id": "test-share-id",
			"size":     10,
		},
		Context: api.ManilaRequestContext{
			ProjectID:       "test-project",
			UserID:          "test-user",
			RequestID:       "req-123",
			GlobalRequestID: "global-req-123",
		},
		Hosts: []api.ExternalSchedulerHost{
			{ShareHost: "manila-share-1@backend1"},
			{ShareHost: "manila-share-2@backend2"},
		},
		Weights:  map[string]float64{"manila-share-1@backend1": 1.0, "manila-share-2@backend2": 0.5},
		Pipeline: "test-pipeline",
	}

	manilaRaw, err := json.Marshal(manilaRequest)
	if err != nil {
		t.Fatalf("Failed to marshal manila request: %v", err)
	}

	tests := []struct {
		name                  string
		decision              *v1alpha1.Decision
		pipelineConfig        *v1alpha1.Pipeline
		createDecisions       bool
		expectError           bool
		expectDecisionCreated bool
		expectResult          bool
		expectDuration        bool
	}{
		{
			name: "successful decision processing with creation",
			decision: &v1alpha1.Decision{
				ObjectMeta: metav1.ObjectMeta{
					GenerateName: "test-decision-",
					Namespace:    "default",
				},
				Spec: v1alpha1.DecisionSpec{
					Type:     v1alpha1.DecisionTypeManilaShare,
					Operator: "test-operator",
					PipelineRef: corev1.ObjectReference{
						Name: "test-pipeline",
					},
					ManilaRaw: &runtime.RawExtension{
						Raw: manilaRaw,
					},
				},
			},
			pipelineConfig: &v1alpha1.Pipeline{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-pipeline",
				},
				Spec: v1alpha1.PipelineSpec{
					Type:            v1alpha1.PipelineTypeFilterWeigher,
					Operator:        "test-operator",
					CreateDecisions: true,
					Steps:           []v1alpha1.StepInPipeline{},
				},
			},
			createDecisions:       true,
			expectError:           false,
			expectDecisionCreated: true,
			expectResult:          true,
			expectDuration:        true,
		},
		{
			name: "successful decision processing without creation",
			decision: &v1alpha1.Decision{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-decision-no-create",
					Namespace: "default",
				},
				Spec: v1alpha1.DecisionSpec{
					Type:     v1alpha1.DecisionTypeManilaShare,
					Operator: "test-operator",
					PipelineRef: corev1.ObjectReference{
						Name: "test-pipeline",
					},
					ManilaRaw: &runtime.RawExtension{
						Raw: manilaRaw,
					},
				},
			},
			pipelineConfig: &v1alpha1.Pipeline{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-pipeline",
				},
				Spec: v1alpha1.PipelineSpec{
					Type:            v1alpha1.PipelineTypeFilterWeigher,
					Operator:        "test-operator",
					CreateDecisions: false,
					Steps:           []v1alpha1.StepInPipeline{},
				},
			},
			createDecisions:       false,
			expectError:           false,
			expectDecisionCreated: false,
			expectResult:          true,
			expectDuration:        true,
		},
		{
			name: "pipeline not configured",
			decision: &v1alpha1.Decision{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-decision-no-pipeline",
					Namespace: "default",
				},
				Spec: v1alpha1.DecisionSpec{
					Type:     v1alpha1.DecisionTypeManilaShare,
					Operator: "test-operator",
					PipelineRef: corev1.ObjectReference{
						Name: "nonexistent-pipeline",
					},
					ManilaRaw: &runtime.RawExtension{
						Raw: manilaRaw,
					},
				},
			},
			pipelineConfig:        nil,
			expectError:           true,
			expectDecisionCreated: false,
			expectResult:          false,
			expectDuration:        false,
		},
		{
			name: "decision without manilaRaw spec",
			decision: &v1alpha1.Decision{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-decision-no-raw",
					Namespace: "default",
				},
				Spec: v1alpha1.DecisionSpec{
					Type:     v1alpha1.DecisionTypeManilaShare,
					Operator: "test-operator",
					PipelineRef: corev1.ObjectReference{
						Name: "test-pipeline",
					},
					ManilaRaw: nil,
				},
			},
			pipelineConfig: &v1alpha1.Pipeline{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-pipeline",
				},
				Spec: v1alpha1.PipelineSpec{
					Type:            v1alpha1.PipelineTypeFilterWeigher,
					Operator:        "test-operator",
					CreateDecisions: true,
					Steps:           []v1alpha1.StepInPipeline{},
				},
			},
			createDecisions:       true,
			expectError:           true,
			expectDecisionCreated: false,
			expectResult:          false,
			expectDuration:        false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			objects := []client.Object{}
			if tt.pipelineConfig != nil {
				objects = append(objects, tt.pipelineConfig)
			}

			client := fake.NewClientBuilder().
				WithScheme(scheme).
				WithObjects(objects...).
				WithStatusSubresource(&v1alpha1.Decision{}).
				Build()

			controller := &DecisionPipelineController{
				BasePipelineController: lib.BasePipelineController[lib.Pipeline[api.ExternalSchedulerRequest]]{
					Client:          client,
					Pipelines:       make(map[string]lib.Pipeline[api.ExternalSchedulerRequest]),
					PipelineConfigs: make(map[string]v1alpha1.Pipeline),
				},
				Monitor: lib.PipelineMonitor{},
				Conf: conf.Config{
					Operator: "test-operator",
				},
			}

			if tt.pipelineConfig != nil {
				controller.PipelineConfigs[tt.pipelineConfig.Name] = *tt.pipelineConfig
				pipeline, err := controller.InitPipeline(t.Context(), tt.pipelineConfig.Name, []v1alpha1.Step{})
				if err != nil {
					t.Fatalf("Failed to init pipeline: %v", err)
				}
				controller.Pipelines[tt.pipelineConfig.Name] = pipeline
			}

			err := controller.ProcessNewDecisionFromAPI(context.Background(), tt.decision)

			if tt.expectError && err == nil {
				t.Error("Expected error but got none")
			}
			if !tt.expectError && err != nil {
				t.Errorf("Expected no error but got: %v", err)
			}

			// Check if decision was created (if expected)
			if tt.expectDecisionCreated {
				var decisions v1alpha1.DecisionList
				err := client.List(context.Background(), &decisions)
				if err != nil {
					t.Errorf("Failed to list decisions: %v", err)
					return
				}

				found := false
				for _, decision := range decisions.Items {
					if decision.Spec.Type == v1alpha1.DecisionTypeManilaShare &&
						decision.Spec.Operator == "test-operator" {
						found = true

						// Verify decision properties
						if decision.Spec.PipelineRef.Name != "test-pipeline" {
							t.Errorf("expected pipeline ref %q, got %q", "test-pipeline", decision.Spec.PipelineRef.Name)
						}

						// Check if result was set
						if tt.expectResult {
							if decision.Status.Result == nil {
								t.Error("expected decision result to be set")
								return
							}
							if tt.expectDuration && decision.Status.Took.Duration <= 0 {
								t.Error("expected duration to be positive")
							}
						}
						break
					}
				}

				if !found {
					t.Error("expected decision to be created but was not found")
				}
			} else if !tt.expectError {
				// For cases without creation, check that the decision has the right status
				if tt.expectResult && tt.decision.Status.Result == nil {
					t.Error("expected decision result to be set in original decision object")
				}
			}
		})
	}
}

func TestDecisionPipelineController_InitPipeline(t *testing.T) {
	controller := &DecisionPipelineController{
		Monitor: lib.PipelineMonitor{},
	}

	tests := []struct {
		name        string
		steps       []v1alpha1.Step
		expectError bool
	}{
		{
			name:        "empty steps",
			steps:       []v1alpha1.Step{},
			expectError: false,
		},
		{
			name: "supported netapp step",
			steps: []v1alpha1.Step{
				{
					ObjectMeta: metav1.ObjectMeta{
						Name: "test-step",
					},
					Spec: v1alpha1.StepSpec{
						Type: v1alpha1.StepTypeWeigher,
						Impl: "netapp_cpu_usage_balancing",
						Opts: runtime.RawExtension{
							Raw: []byte(`{"AvgCPUUsageLowerBound": 0, "AvgCPUUsageUpperBound": 90, "MaxCPUUsageLowerBound": 0, "MaxCPUUsageUpperBound": 100}`),
						},
					},
				},
			},
			expectError: false,
		},
		{
			name: "unsupported step",
			steps: []v1alpha1.Step{
				{
					ObjectMeta: metav1.ObjectMeta{
						Name: "test-step",
					},
					Spec: v1alpha1.StepSpec{
						Type: v1alpha1.StepTypeFilter,
						Impl: "unsupported-plugin",
					},
				},
			},
			expectError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			pipeline, err := controller.InitPipeline(t.Context(), "test", tt.steps)

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
