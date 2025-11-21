// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package nova

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	api "github.com/cobaltcore-dev/cortex/api/delegation/nova"
	"github.com/cobaltcore-dev/cortex/api/v1alpha1"

	"github.com/cobaltcore-dev/cortex/internal/scheduling/lib"
	"github.com/cobaltcore-dev/cortex/pkg/conf"
)

func TestDecisionPipelineController_Reconcile(t *testing.T) {
	scheme := runtime.NewScheme()
	if err := v1alpha1.AddToScheme(scheme); err != nil {
		t.Fatalf("Failed to add v1alpha1 scheme: %v", err)
	}

	novaRequest := api.ExternalSchedulerRequest{
		Spec: api.NovaObject[api.NovaSpec]{
			Name:      "RequestSpec",
			Namespace: "nova_object",
			Version:   "1.19",
			Data: api.NovaSpec{
				ProjectID:    "test-project",
				UserID:       "test-user",
				InstanceUUID: "test-instance-uuid",
				NumInstances: 1,
			},
		},
		Context: api.NovaRequestContext{
			ProjectID:       "test-project",
			UserID:          "test-user",
			RequestID:       "req-123",
			GlobalRequestID: func() *string { s := "global-req-123"; return &s }(),
		},
		Hosts: []api.ExternalSchedulerHost{
			{ComputeHost: "compute-1", HypervisorHostname: "hv-1"},
			{ComputeHost: "compute-2", HypervisorHostname: "hv-2"},
		},
		Weights:  map[string]float64{"compute-1": 1.0, "compute-2": 0.5},
		Pipeline: "test-pipeline",
	}

	novaRaw, err := json.Marshal(novaRequest)
	if err != nil {
		t.Fatalf("Failed to marshal nova request: %v", err)
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
			name: "successful nova decision processing",
			decision: &v1alpha1.Decision{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-decision",
					Namespace: "default",
				},
				Spec: v1alpha1.DecisionSpec{
					Type:     v1alpha1.DecisionTypeNovaServer,
					Operator: "test-operator",
					PipelineRef: corev1.ObjectReference{
						Name: "test-pipeline",
					},
					NovaRaw: &runtime.RawExtension{
						Raw: novaRaw,
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
			name: "decision without novaRaw spec",
			decision: &v1alpha1.Decision{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-decision-no-raw",
					Namespace: "default",
				},
				Spec: v1alpha1.DecisionSpec{
					Type:     v1alpha1.DecisionTypeNovaServer,
					Operator: "test-operator",
					PipelineRef: corev1.ObjectReference{
						Name: "test-pipeline",
					},
					NovaRaw: nil,
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
					Type:     v1alpha1.DecisionTypeNovaServer,
					Operator: "test-operator",
					PipelineRef: corev1.ObjectReference{
						Name: "nonexistent-pipeline",
					},
					NovaRaw: &runtime.RawExtension{
						Raw: novaRaw,
					},
				},
			},
			pipeline:       nil,
			expectError:    true,
			expectResult:   false,
			expectDuration: false,
		},
		{
			name: "invalid novaRaw JSON",
			decision: &v1alpha1.Decision{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-decision-invalid-json",
					Namespace: "default",
				},
				Spec: v1alpha1.DecisionSpec{
					Type:     v1alpha1.DecisionTypeNovaServer,
					Operator: "test-operator",
					PipelineRef: corev1.ObjectReference{
						Name: "test-pipeline",
					},
					NovaRaw: &runtime.RawExtension{
						Raw: []byte("invalid json"),
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
				if !tt.expectError {
					t.Fatalf("Failed to get updated decision: %v", err)
				}
				return
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
			name: "supported step",
			steps: []v1alpha1.Step{
				{
					ObjectMeta: metav1.ObjectMeta{
						Name: "filter_disabled",
					},
					Spec: v1alpha1.StepSpec{
						Type: v1alpha1.StepTypeFilter,
						Impl: "filter_disabled",
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
		{
			name: "step with scoping options",
			steps: []v1alpha1.Step{
				{
					ObjectMeta: metav1.ObjectMeta{
						Name: "scoped-filter",
					},
					Spec: v1alpha1.StepSpec{
						Type: v1alpha1.StepTypeFilter,
						Impl: "filter_disabled",
						Opts: runtime.RawExtension{
							Raw: []byte(`{"scope":{"host_capabilities":{"any_of_trait_infixes":["TEST_TRAIT"]}}}`),
						},
					},
				},
			},
			expectError: false,
		},
		{
			name: "step with invalid scoping options",
			steps: []v1alpha1.Step{
				{
					ObjectMeta: metav1.ObjectMeta{
						Name: "invalid-scoped-filter",
					},
					Spec: v1alpha1.StepSpec{
						Type: v1alpha1.StepTypeFilter,
						Impl: "filter_disabled",
						Opts: runtime.RawExtension{
							Raw: []byte(`invalid json`),
						},
					},
				},
			},
			expectError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			pipeline, err := controller.InitPipeline(t.Context(), "test-pipeline", tt.steps)

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

func TestDecisionPipelineController_ProcessNewDecisionFromAPI(t *testing.T) {
	scheme := runtime.NewScheme()
	if err := v1alpha1.AddToScheme(scheme); err != nil {
		t.Fatalf("Failed to add v1alpha1 scheme: %v", err)
	}

	novaRequest := api.ExternalSchedulerRequest{
		Spec: api.NovaObject[api.NovaSpec]{
			Name:      "RequestSpec",
			Namespace: "nova_object",
			Version:   "1.19",
			Data: api.NovaSpec{
				ProjectID:    "test-project",
				UserID:       "test-user",
				InstanceUUID: "test-instance-uuid",
				NumInstances: 1,
			},
		},
		Context: api.NovaRequestContext{
			ProjectID:       "test-project",
			UserID:          "test-user",
			RequestID:       "req-123",
			GlobalRequestID: func() *string { s := "global-req-123"; return &s }(),
		},
		Hosts: []api.ExternalSchedulerHost{
			{ComputeHost: "compute-1", HypervisorHostname: "hv-1"},
			{ComputeHost: "compute-2", HypervisorHostname: "hv-2"},
		},
		Weights:  map[string]float64{"compute-1": 1.0, "compute-2": 0.5},
		Pipeline: "test-pipeline",
	}

	novaRaw, err := json.Marshal(novaRequest)
	if err != nil {
		t.Fatalf("Failed to marshal nova request: %v", err)
	}

	tests := []struct {
		name                  string
		decision              *v1alpha1.Decision
		pipeline              *v1alpha1.Pipeline
		pipelineConf          *v1alpha1.Pipeline
		setupPipelineConfigs  bool
		createDecisions       bool
		expectError           bool
		expectResult          bool
		expectDuration        bool
		expectCreatedDecision bool
		expectUpdatedStatus   bool
		errorContains         string
	}{
		{
			name: "successful processing with decision creation enabled",
			decision: &v1alpha1.Decision{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-decision-api",
					Namespace: "default",
				},
				Spec: v1alpha1.DecisionSpec{
					Type:     v1alpha1.DecisionTypeNovaServer,
					Operator: "test-operator",
					PipelineRef: corev1.ObjectReference{
						Name: "test-pipeline",
					},
					NovaRaw: &runtime.RawExtension{
						Raw: novaRaw,
					},
				},
			},
			pipeline: &v1alpha1.Pipeline{
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
			pipelineConf: &v1alpha1.Pipeline{
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
			setupPipelineConfigs:  true,
			createDecisions:       true,
			expectError:           false,
			expectResult:          true,
			expectDuration:        true,
			expectCreatedDecision: true,
			expectUpdatedStatus:   true,
		},
		{
			name: "successful processing with decision creation disabled",
			decision: &v1alpha1.Decision{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-decision-no-create",
					Namespace: "default",
				},
				Spec: v1alpha1.DecisionSpec{
					Type:     v1alpha1.DecisionTypeNovaServer,
					Operator: "test-operator",
					PipelineRef: corev1.ObjectReference{
						Name: "test-pipeline-no-create",
					},
					NovaRaw: &runtime.RawExtension{
						Raw: novaRaw,
					},
				},
			},
			pipeline: &v1alpha1.Pipeline{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-pipeline-no-create",
				},
				Spec: v1alpha1.PipelineSpec{
					Type:            v1alpha1.PipelineTypeFilterWeigher,
					Operator:        "test-operator",
					CreateDecisions: false,
					Steps:           []v1alpha1.StepInPipeline{},
				},
			},
			pipelineConf: &v1alpha1.Pipeline{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-pipeline-no-create",
				},
				Spec: v1alpha1.PipelineSpec{
					Type:            v1alpha1.PipelineTypeFilterWeigher,
					Operator:        "test-operator",
					CreateDecisions: false,
					Steps:           []v1alpha1.StepInPipeline{},
				},
			},
			setupPipelineConfigs:  true,
			createDecisions:       false,
			expectError:           false,
			expectResult:          true,
			expectDuration:        true,
			expectCreatedDecision: false,
			expectUpdatedStatus:   false,
		},
		{
			name: "pipeline not configured",
			decision: &v1alpha1.Decision{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-decision-no-config",
					Namespace: "default",
				},
				Spec: v1alpha1.DecisionSpec{
					Type:     v1alpha1.DecisionTypeNovaServer,
					Operator: "test-operator",
					PipelineRef: corev1.ObjectReference{
						Name: "nonexistent-pipeline",
					},
					NovaRaw: &runtime.RawExtension{
						Raw: novaRaw,
					},
				},
			},
			pipeline:              nil,
			pipelineConf:          nil,
			setupPipelineConfigs:  false,
			expectError:           true,
			expectResult:          false,
			expectDuration:        false,
			expectCreatedDecision: false,
			expectUpdatedStatus:   false,
			errorContains:         "pipeline nonexistent-pipeline not configured",
		},
		{
			name: "decision without novaRaw spec",
			decision: &v1alpha1.Decision{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-decision-no-raw-api",
					Namespace: "default",
				},
				Spec: v1alpha1.DecisionSpec{
					Type:     v1alpha1.DecisionTypeNovaServer,
					Operator: "test-operator",
					PipelineRef: corev1.ObjectReference{
						Name: "test-pipeline",
					},
					NovaRaw: nil,
				},
			},
			pipeline: &v1alpha1.Pipeline{
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
			pipelineConf: &v1alpha1.Pipeline{
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
			setupPipelineConfigs:  true,
			createDecisions:       true,
			expectError:           true,
			expectResult:          false,
			expectDuration:        false,
			expectCreatedDecision: true,
			expectUpdatedStatus:   false,
			errorContains:         "no novaRaw spec defined",
		},
		{
			name: "processing fails after decision creation",
			decision: &v1alpha1.Decision{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-decision-process-fail",
					Namespace: "default",
				},
				Spec: v1alpha1.DecisionSpec{
					Type:     v1alpha1.DecisionTypeNovaServer,
					Operator: "test-operator",
					PipelineRef: corev1.ObjectReference{
						Name: "test-pipeline",
					},
					NovaRaw: &runtime.RawExtension{
						Raw: novaRaw,
					},
				},
			},
			pipeline: nil, // This will cause processing to fail after creation
			pipelineConf: &v1alpha1.Pipeline{
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
			setupPipelineConfigs:  true,
			createDecisions:       true,
			expectError:           true,
			expectResult:          false,
			expectDuration:        false,
			expectCreatedDecision: true,
			expectUpdatedStatus:   false,
			errorContains:         "pipeline not found or not ready",
		},
		{
			name: "pipeline not found in runtime map",
			decision: &v1alpha1.Decision{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-decision-no-runtime-pipeline",
					Namespace: "default",
				},
				Spec: v1alpha1.DecisionSpec{
					Type:     v1alpha1.DecisionTypeNovaServer,
					Operator: "test-operator",
					PipelineRef: corev1.ObjectReference{
						Name: "missing-runtime-pipeline",
					},
					NovaRaw: &runtime.RawExtension{
						Raw: novaRaw,
					},
				},
			},
			pipeline: nil,
			pipelineConf: &v1alpha1.Pipeline{
				ObjectMeta: metav1.ObjectMeta{
					Name: "missing-runtime-pipeline",
				},
				Spec: v1alpha1.PipelineSpec{
					Type:            v1alpha1.PipelineTypeFilterWeigher,
					Operator:        "test-operator",
					CreateDecisions: true,
					Steps:           []v1alpha1.StepInPipeline{},
				},
			},
			setupPipelineConfigs:  true,
			createDecisions:       true,
			expectError:           true,
			expectResult:          false,
			expectDuration:        false,
			expectCreatedDecision: true,
			expectUpdatedStatus:   false,
			errorContains:         "pipeline not found or not ready",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			objects := []client.Object{}
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
					Client:          client,
					Pipelines:       make(map[string]lib.Pipeline[api.ExternalSchedulerRequest]),
					PipelineConfigs: make(map[string]v1alpha1.Pipeline),
				},
				Monitor: lib.PipelineMonitor{},
				Conf: conf.Config{
					Operator: "test-operator",
				},
			}

			// Setup pipeline configurations if needed
			if tt.setupPipelineConfigs && tt.pipelineConf != nil {
				controller.PipelineConfigs[tt.pipelineConf.Name] = *tt.pipelineConf
			}

			// Setup runtime pipeline if needed
			if tt.pipeline != nil {
				pipeline, err := controller.InitPipeline(context.Background(), tt.pipeline.Name, []v1alpha1.Step{})
				if err != nil {
					t.Fatalf("Failed to init pipeline: %v", err)
				}
				controller.Pipelines[tt.pipeline.Name] = pipeline
			}

			// Call the method under test
			err := controller.ProcessNewDecisionFromAPI(context.Background(), tt.decision)

			// Validate error expectations
			if tt.expectError && err == nil {
				t.Error("Expected error but got none")
			}
			if !tt.expectError && err != nil {
				t.Errorf("Expected no error but got: %v", err)
			}
			if tt.errorContains != "" && (err == nil || !strings.Contains(err.Error(), tt.errorContains)) {
				t.Errorf("Expected error to contain %q, got: %v", tt.errorContains, err)
			}

			// Check if decision was created in the cluster when expected
			if tt.expectCreatedDecision {
				var createdDecision v1alpha1.Decision
				key := types.NamespacedName{Name: tt.decision.Name, Namespace: tt.decision.Namespace}
				err := client.Get(context.Background(), key, &createdDecision)
				if err != nil {
					t.Errorf("Expected decision to be created but got error: %v", err)
				}
			} else {
				var createdDecision v1alpha1.Decision
				key := types.NamespacedName{Name: tt.decision.Name, Namespace: tt.decision.Namespace}
				err := client.Get(context.Background(), key, &createdDecision)
				if err == nil {
					t.Error("Expected decision not to be created but it was found")
				}
			}

			// Validate result and duration expectations
			if tt.expectResult && tt.decision.Status.Result == nil {
				t.Error("Expected result to be set but was nil")
			}
			if !tt.expectResult && tt.decision.Status.Result != nil {
				t.Error("Expected result to be nil but was set")
			}

			if tt.expectDuration && tt.decision.Status.Took.Duration == 0 {
				t.Error("Expected duration to be set but was zero")
			}
			if !tt.expectDuration && tt.decision.Status.Took.Duration != 0 {
				t.Error("Expected duration to be zero but was set")
			}
		})
	}
}
