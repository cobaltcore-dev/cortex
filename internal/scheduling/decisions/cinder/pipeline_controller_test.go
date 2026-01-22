// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package cinder

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

	api "github.com/cobaltcore-dev/cortex/api/delegation/cinder"
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

	cinderRequest := api.ExternalSchedulerRequest{
		Spec: map[string]any{
			"volume_id": "test-volume-id",
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
	}

	cinderRaw, err := json.Marshal(cinderRequest)
	if err != nil {
		t.Fatalf("Failed to marshal cinder request: %v", err)
	}

	tests := []struct {
		name         string
		decision     *v1alpha1.Decision
		pipeline     *v1alpha1.Pipeline
		expectError  bool
		expectResult bool
	}{
		{
			name: "successful cinder decision processing",
			decision: &v1alpha1.Decision{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-decision",
					Namespace: "default",
				},
				Spec: v1alpha1.DecisionSpec{
					SchedulingDomain: v1alpha1.SchedulingDomainCinder,
					PipelineRef: corev1.ObjectReference{
						Name: "test-pipeline",
					},
					CinderRaw: &runtime.RawExtension{
						Raw: cinderRaw,
					},
				},
			},
			pipeline: &v1alpha1.Pipeline{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-pipeline",
				},
				Spec: v1alpha1.PipelineSpec{
					Type:             v1alpha1.PipelineTypeFilterWeigher,
					SchedulingDomain: v1alpha1.SchedulingDomainCinder,
					Filters:          []v1alpha1.StepSpec{},
					Weighers:         []v1alpha1.StepSpec{},
				},
			},
			expectError:  false,
			expectResult: true,
		},
		{
			name: "decision without cinderRaw spec",
			decision: &v1alpha1.Decision{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-decision-no-raw",
					Namespace: "default",
				},
				Spec: v1alpha1.DecisionSpec{
					SchedulingDomain: v1alpha1.SchedulingDomainCinder,
					PipelineRef: corev1.ObjectReference{
						Name: "test-pipeline",
					},
					CinderRaw: nil,
				},
			},
			pipeline: &v1alpha1.Pipeline{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-pipeline",
				},
				Spec: v1alpha1.PipelineSpec{
					Type:             v1alpha1.PipelineTypeFilterWeigher,
					SchedulingDomain: v1alpha1.SchedulingDomainCinder,
					Filters:          []v1alpha1.StepSpec{},
					Weighers:         []v1alpha1.StepSpec{},
				},
			},
			expectError:  true,
			expectResult: false,
		},
		{
			name: "pipeline not found",
			decision: &v1alpha1.Decision{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-decision-no-pipeline",
					Namespace: "default",
				},
				Spec: v1alpha1.DecisionSpec{
					SchedulingDomain: v1alpha1.SchedulingDomainCinder,
					PipelineRef: corev1.ObjectReference{
						Name: "nonexistent-pipeline",
					},
					CinderRaw: &runtime.RawExtension{
						Raw: cinderRaw,
					},
				},
			},
			pipeline:     nil,
			expectError:  true,
			expectResult: false,
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
					SchedulingDomain: v1alpha1.SchedulingDomainCinder,
				},
			}

			if tt.pipeline != nil {
				pipeline, err := controller.InitPipeline(t.Context(), v1alpha1.Pipeline{
					ObjectMeta: metav1.ObjectMeta{
						Name: "test-pipeline",
					},
					Spec: v1alpha1.PipelineSpec{
						Type:             v1alpha1.PipelineTypeFilterWeigher,
						SchedulingDomain: v1alpha1.SchedulingDomainCinder,
						Filters:          []v1alpha1.StepSpec{},
						Weighers:         []v1alpha1.StepSpec{},
					},
				})
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
		})
	}
}

func TestDecisionPipelineController_ProcessNewDecisionFromAPI(t *testing.T) {
	scheme := runtime.NewScheme()
	if err := v1alpha1.AddToScheme(scheme); err != nil {
		t.Fatalf("Failed to add v1alpha1 scheme: %v", err)
	}

	cinderRequest := api.ExternalSchedulerRequest{
		Spec: map[string]any{
			"volume_id": "test-volume-id",
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
	}

	cinderRaw, err := json.Marshal(cinderRequest)
	if err != nil {
		t.Fatalf("Failed to marshal cinder request: %v", err)
	}

	tests := []struct {
		name                  string
		decision              *v1alpha1.Decision
		pipelineConfig        *v1alpha1.Pipeline
		createDecisions       bool
		expectError           bool
		expectDecisionCreated bool
		expectResult          bool
	}{
		{
			name: "successful decision processing with creation",
			decision: &v1alpha1.Decision{
				ObjectMeta: metav1.ObjectMeta{
					GenerateName: "test-decision-",
					Namespace:    "default",
				},
				Spec: v1alpha1.DecisionSpec{
					SchedulingDomain: v1alpha1.SchedulingDomainCinder,
					PipelineRef: corev1.ObjectReference{
						Name: "test-pipeline",
					},
					CinderRaw: &runtime.RawExtension{
						Raw: cinderRaw,
					},
				},
			},
			pipelineConfig: &v1alpha1.Pipeline{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-pipeline",
				},
				Spec: v1alpha1.PipelineSpec{
					Type:             v1alpha1.PipelineTypeFilterWeigher,
					SchedulingDomain: v1alpha1.SchedulingDomainCinder,
					CreateDecisions:  true,
					Filters:          []v1alpha1.StepSpec{},
					Weighers:         []v1alpha1.StepSpec{},
				},
			},
			createDecisions:       true,
			expectError:           false,
			expectDecisionCreated: true,
			expectResult:          true,
		},
		{
			name: "successful decision processing without creation",
			decision: &v1alpha1.Decision{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-decision-no-create",
					Namespace: "default",
				},
				Spec: v1alpha1.DecisionSpec{
					SchedulingDomain: v1alpha1.SchedulingDomainCinder,
					PipelineRef: corev1.ObjectReference{
						Name: "test-pipeline",
					},
					CinderRaw: &runtime.RawExtension{
						Raw: cinderRaw,
					},
				},
			},
			pipelineConfig: &v1alpha1.Pipeline{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-pipeline",
				},
				Spec: v1alpha1.PipelineSpec{
					Type:             v1alpha1.PipelineTypeFilterWeigher,
					SchedulingDomain: v1alpha1.SchedulingDomainCinder,
					CreateDecisions:  false,
					Filters:          []v1alpha1.StepSpec{},
					Weighers:         []v1alpha1.StepSpec{},
				},
			},
			createDecisions:       false,
			expectError:           false,
			expectDecisionCreated: false,
			expectResult:          true,
		},
		{
			name: "pipeline not configured",
			decision: &v1alpha1.Decision{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-decision-no-pipeline",
					Namespace: "default",
				},
				Spec: v1alpha1.DecisionSpec{
					SchedulingDomain: v1alpha1.SchedulingDomainCinder,
					PipelineRef: corev1.ObjectReference{
						Name: "nonexistent-pipeline",
					},
					CinderRaw: &runtime.RawExtension{
						Raw: cinderRaw,
					},
				},
			},
			pipelineConfig:        nil,
			expectError:           true,
			expectDecisionCreated: false,
			expectResult:          false,
		},
		{
			name: "decision without cinderRaw spec",
			decision: &v1alpha1.Decision{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-decision-no-raw",
					Namespace: "default",
				},
				Spec: v1alpha1.DecisionSpec{
					SchedulingDomain: v1alpha1.SchedulingDomainCinder,
					PipelineRef: corev1.ObjectReference{
						Name: "test-pipeline",
					},
					CinderRaw: nil,
				},
			},
			pipelineConfig: &v1alpha1.Pipeline{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-pipeline",
				},
				Spec: v1alpha1.PipelineSpec{
					Type:             v1alpha1.PipelineTypeFilterWeigher,
					SchedulingDomain: v1alpha1.SchedulingDomainCinder,
					CreateDecisions:  true,
					Filters:          []v1alpha1.StepSpec{},
					Weighers:         []v1alpha1.StepSpec{},
				},
			},
			createDecisions:       true,
			expectError:           true,
			expectDecisionCreated: false,
			expectResult:          false,
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
					SchedulingDomain: v1alpha1.SchedulingDomainCinder,
				},
			}

			if tt.pipelineConfig != nil {
				controller.PipelineConfigs[tt.pipelineConfig.Name] = *tt.pipelineConfig
				pipeline, err := controller.InitPipeline(t.Context(), *tt.pipelineConfig)
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
					if decision.Spec.SchedulingDomain == v1alpha1.SchedulingDomainCinder {
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
		filters     []v1alpha1.StepSpec
		weighers    []v1alpha1.StepSpec
		expectError bool
	}{
		{
			name:        "empty steps",
			filters:     []v1alpha1.StepSpec{},
			weighers:    []v1alpha1.StepSpec{},
			expectError: false,
		},
		{
			name: "unsupported step",
			filters: []v1alpha1.StepSpec{
				{
					Name: "test-plugin",
				},
			},
			weighers: []v1alpha1.StepSpec{
				{
					Name: "test-plugin",
				},
			},
			expectError: true, // Expected because test-plugin is not in supportedSteps
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			pipeline, err := controller.InitPipeline(t.Context(), v1alpha1.Pipeline{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-pipeline",
				},
				Spec: v1alpha1.PipelineSpec{
					Type:             v1alpha1.PipelineTypeFilterWeigher,
					SchedulingDomain: v1alpha1.SchedulingDomainCinder,
					Filters:          tt.filters,
					Weighers:         tt.weighers,
				},
			})

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
