// Copyright SAP SE
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

func TestFilterWeigherPipelineController_Reconcile(t *testing.T) {
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
		name         string
		decision     *v1alpha1.Decision
		pipeline     *v1alpha1.Pipeline
		expectError  bool
		expectResult bool
	}{
		{
			name: "successful nova decision processing",
			decision: &v1alpha1.Decision{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-decision",
					Namespace: "default",
				},
				Spec: v1alpha1.DecisionSpec{
					SchedulingDomain: v1alpha1.SchedulingDomainNova,
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
					Type:             v1alpha1.PipelineTypeFilterWeigher,
					SchedulingDomain: v1alpha1.SchedulingDomainNova,
					Filters:          []v1alpha1.FilterSpec{},
					Weighers:         []v1alpha1.WeigherSpec{},
				},
			},
			expectError:  false,
			expectResult: true,
		},
		{
			name: "decision without novaRaw spec",
			decision: &v1alpha1.Decision{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-decision-no-raw",
					Namespace: "default",
				},
				Spec: v1alpha1.DecisionSpec{
					SchedulingDomain: v1alpha1.SchedulingDomainNova,
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
					Type:             v1alpha1.PipelineTypeFilterWeigher,
					SchedulingDomain: v1alpha1.SchedulingDomainNova,
					Filters:          []v1alpha1.FilterSpec{},
					Weighers:         []v1alpha1.WeigherSpec{},
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
					SchedulingDomain: v1alpha1.SchedulingDomainNova,
					PipelineRef: corev1.ObjectReference{
						Name: "nonexistent-pipeline",
					},
					NovaRaw: &runtime.RawExtension{
						Raw: novaRaw,
					},
				},
			},
			pipeline:     nil,
			expectError:  true,
			expectResult: false,
		},
		{
			name: "invalid novaRaw JSON",
			decision: &v1alpha1.Decision{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-decision-invalid-json",
					Namespace: "default",
				},
				Spec: v1alpha1.DecisionSpec{
					SchedulingDomain: v1alpha1.SchedulingDomainNova,
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
					Type:             v1alpha1.PipelineTypeFilterWeigher,
					SchedulingDomain: v1alpha1.SchedulingDomainNova,
					Filters:          []v1alpha1.FilterSpec{},
					Weighers:         []v1alpha1.WeigherSpec{},
				},
			},
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

			controller := &FilterWeigherPipelineController{
				BasePipelineController: lib.BasePipelineController[lib.FilterWeigherPipeline[api.ExternalSchedulerRequest]]{
					Client:    client,
					Pipelines: make(map[string]lib.FilterWeigherPipeline[api.ExternalSchedulerRequest]),
				},
				Monitor: lib.FilterWeigherPipelineMonitor{},
				Conf: conf.Config{
					SchedulingDomain: v1alpha1.SchedulingDomainNova,
				},
			}

			if tt.pipeline != nil {
				initResult := controller.InitPipeline(t.Context(), v1alpha1.Pipeline{
					ObjectMeta: metav1.ObjectMeta{
						Name: tt.pipeline.Name,
					},
					Spec: tt.pipeline.Spec,
				})
				if initResult.CriticalErr != nil || initResult.NonCriticalErr != nil {
					t.Fatalf("Failed to init pipeline: %v", err)
				}
				controller.Pipelines[tt.pipeline.Name] = initResult.Pipeline
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
		})
	}
}

func TestFilterWeigherPipelineController_InitPipeline(t *testing.T) {
	controller := &FilterWeigherPipelineController{
		Monitor: lib.FilterWeigherPipelineMonitor{},
	}

	tests := []struct {
		name                   string
		filters                []v1alpha1.FilterSpec
		weighers               []v1alpha1.WeigherSpec
		expectNonCriticalError bool
		expectCriticalError    bool
	}{
		{
			name:                   "empty steps",
			filters:                []v1alpha1.FilterSpec{},
			weighers:               []v1alpha1.WeigherSpec{},
			expectNonCriticalError: false,
			expectCriticalError:    false,
		},
		{
			name: "supported step",
			filters: []v1alpha1.FilterSpec{
				{
					Name: "filter_status_conditions",
				},
			},
			expectNonCriticalError: false,
			expectCriticalError:    false,
		},
		{
			name: "unsupported step",
			filters: []v1alpha1.FilterSpec{
				{
					Name: "unsupported-plugin",
				},
			},
			expectNonCriticalError: false,
			expectCriticalError:    true,
		},
		{
			name: "step with scoping options",
			filters: []v1alpha1.FilterSpec{
				{
					Name: "filter_status_conditions",
					Params: runtime.RawExtension{
						Raw: []byte(`{"scope":{"host_capabilities":{"any_of_trait_infixes":["TEST_TRAIT"]}}}`),
					},
				},
			},
			expectNonCriticalError: false,
			expectCriticalError:    false,
		},
		{
			name: "step with invalid scoping options",
			filters: []v1alpha1.FilterSpec{
				{
					Name: "filter_status_conditions",
					Params: runtime.RawExtension{
						Raw: []byte(`invalid json`),
					},
				},
			},
			expectNonCriticalError: false,
			expectCriticalError:    true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			initResult := controller.InitPipeline(t.Context(), v1alpha1.Pipeline{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-pipeline",
				},
				Spec: v1alpha1.PipelineSpec{
					Filters:  tt.filters,
					Weighers: tt.weighers,
				},
			})

			if tt.expectCriticalError && initResult.CriticalErr == nil {
				t.Error("Expected critical error but got none")
			}
			if !tt.expectCriticalError && initResult.CriticalErr != nil {
				t.Errorf("Expected no critical error but got: %v", initResult.CriticalErr)
			}
			if tt.expectNonCriticalError && initResult.NonCriticalErr == nil {
				t.Error("Expected non-critical error but got none")
			}
			if !tt.expectNonCriticalError && initResult.NonCriticalErr != nil {
				t.Errorf("Expected no non-critical error but got: %v", initResult.NonCriticalErr)
			}
		})
	}
}

func TestFilterWeigherPipelineController_ProcessNewDecisionFromAPI(t *testing.T) {
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
					SchedulingDomain: v1alpha1.SchedulingDomainNova,
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
					Type:             v1alpha1.PipelineTypeFilterWeigher,
					SchedulingDomain: v1alpha1.SchedulingDomainNova,
					CreateDecisions:  true,
					Filters:          []v1alpha1.FilterSpec{},
					Weighers:         []v1alpha1.WeigherSpec{},
				},
			},
			pipelineConf: &v1alpha1.Pipeline{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-pipeline",
				},
				Spec: v1alpha1.PipelineSpec{
					Type:             v1alpha1.PipelineTypeFilterWeigher,
					SchedulingDomain: v1alpha1.SchedulingDomainNova,
					CreateDecisions:  true,
					Filters:          []v1alpha1.FilterSpec{},
					Weighers:         []v1alpha1.WeigherSpec{},
				},
			},
			setupPipelineConfigs:  true,
			createDecisions:       true,
			expectError:           false,
			expectResult:          true,
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
					SchedulingDomain: v1alpha1.SchedulingDomainNova,
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
					Type:             v1alpha1.PipelineTypeFilterWeigher,
					SchedulingDomain: v1alpha1.SchedulingDomainNova,
					CreateDecisions:  false,
					Filters:          []v1alpha1.FilterSpec{},
					Weighers:         []v1alpha1.WeigherSpec{},
				},
			},
			pipelineConf: &v1alpha1.Pipeline{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-pipeline-no-create",
				},
				Spec: v1alpha1.PipelineSpec{
					Type:             v1alpha1.PipelineTypeFilterWeigher,
					SchedulingDomain: v1alpha1.SchedulingDomainNova,
					CreateDecisions:  false,
					Filters:          []v1alpha1.FilterSpec{},
					Weighers:         []v1alpha1.WeigherSpec{},
				},
			},
			setupPipelineConfigs:  true,
			createDecisions:       false,
			expectError:           false,
			expectResult:          true,
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
					SchedulingDomain: v1alpha1.SchedulingDomainNova,
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
					SchedulingDomain: v1alpha1.SchedulingDomainNova,
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
					Type:             v1alpha1.PipelineTypeFilterWeigher,
					SchedulingDomain: v1alpha1.SchedulingDomainNova,
					CreateDecisions:  true,
					Filters:          []v1alpha1.FilterSpec{},
					Weighers:         []v1alpha1.WeigherSpec{},
				},
			},
			pipelineConf: &v1alpha1.Pipeline{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-pipeline",
				},
				Spec: v1alpha1.PipelineSpec{
					Type:             v1alpha1.PipelineTypeFilterWeigher,
					SchedulingDomain: v1alpha1.SchedulingDomainNova,
					CreateDecisions:  true,
					Filters:          []v1alpha1.FilterSpec{},
					Weighers:         []v1alpha1.WeigherSpec{},
				},
			},
			setupPipelineConfigs:  true,
			createDecisions:       true,
			expectError:           true,
			expectResult:          false,
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
					SchedulingDomain: v1alpha1.SchedulingDomainNova,
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
					Type:             v1alpha1.PipelineTypeFilterWeigher,
					SchedulingDomain: v1alpha1.SchedulingDomainNova,
					CreateDecisions:  true,
					Filters:          []v1alpha1.FilterSpec{},
					Weighers:         []v1alpha1.WeigherSpec{},
				},
			},
			setupPipelineConfigs:  true,
			createDecisions:       true,
			expectError:           true,
			expectResult:          false,
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
					SchedulingDomain: v1alpha1.SchedulingDomainNova,
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
					Type:             v1alpha1.PipelineTypeFilterWeigher,
					SchedulingDomain: v1alpha1.SchedulingDomainNova,
					CreateDecisions:  true,
					Filters:          []v1alpha1.FilterSpec{},
					Weighers:         []v1alpha1.WeigherSpec{},
				},
			},
			setupPipelineConfigs:  true,
			createDecisions:       true,
			expectError:           true,
			expectResult:          false,
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

			controller := &FilterWeigherPipelineController{
				BasePipelineController: lib.BasePipelineController[lib.FilterWeigherPipeline[api.ExternalSchedulerRequest]]{
					Client:          client,
					Pipelines:       make(map[string]lib.FilterWeigherPipeline[api.ExternalSchedulerRequest]),
					PipelineConfigs: make(map[string]v1alpha1.Pipeline),
				},
				Monitor: lib.FilterWeigherPipelineMonitor{},
				Conf: conf.Config{
					SchedulingDomain: v1alpha1.SchedulingDomainNova,
				},
			}

			// Setup pipeline configurations if needed
			if tt.setupPipelineConfigs && tt.pipelineConf != nil {
				controller.PipelineConfigs[tt.pipelineConf.Name] = *tt.pipelineConf
			}

			// Setup runtime pipeline if needed
			if tt.pipeline != nil {
				initResult := controller.InitPipeline(context.Background(), v1alpha1.Pipeline{
					ObjectMeta: metav1.ObjectMeta{
						Name: tt.pipeline.Name,
					},
					Spec: tt.pipeline.Spec,
				})
				if initResult.CriticalErr != nil || initResult.NonCriticalErr != nil {
					t.Fatalf("Failed to init pipeline: %v", initResult)
				}
				controller.Pipelines[tt.pipeline.Name] = initResult.Pipeline
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
		})
	}
}
