// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package nova

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	knowledgev1alpha1 "github.com/cobaltcore-dev/cortex/knowledge/api/v1alpha1"
	"github.com/cobaltcore-dev/cortex/lib/db"
	api "github.com/cobaltcore-dev/cortex/scheduling/api/delegation/nova"
	"github.com/cobaltcore-dev/cortex/scheduling/api/v1alpha1"
	"github.com/cobaltcore-dev/cortex/scheduling/internal/conf"
	"github.com/cobaltcore-dev/cortex/scheduling/internal/lib"
	testlibDB "github.com/cobaltcore-dev/cortex/testlib/db"
)

func TestDecisionPipelineController_Reconcile(t *testing.T) {
	scheme := runtime.NewScheme()
	if err := v1alpha1.AddToScheme(scheme); err != nil {
		t.Fatalf("Failed to add v1alpha1 scheme: %v", err)
	}
	if err := knowledgev1alpha1.AddToScheme(scheme); err != nil {
		t.Fatalf("Failed to add knowledgev1alpha1 scheme: %v", err)
	}

	dbEnv := testlibDB.SetupDBEnv(t)
	testDB := db.DB{DbMap: dbEnv.DbMap}
	defer testDB.Close()
	defer dbEnv.Close()

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
			expectError:    false,
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
			expectError:    false,
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
				DB:              testDB,
				Monitor:         lib.PipelineMonitor{},
				pendingRequests: make(map[string]*pendingRequest),
				Conf: conf.Config{
					Operator: "test-operator",
				},
			}

			if tt.pipeline != nil {
				pipeline, err := controller.InitPipeline([]v1alpha1.Step{})
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

func TestDecisionPipelineController_ProcessNewDecisionFromAPI(t *testing.T) {
	scheme := runtime.NewScheme()
	if err := v1alpha1.AddToScheme(scheme); err != nil {
		t.Fatalf("Failed to add scheme: %v", err)
	}

	dbEnv := testlibDB.SetupDBEnv(t)
	testDB := db.DB{DbMap: dbEnv.DbMap}
	defer testDB.Close()
	defer dbEnv.Close()

	tests := []struct {
		name           string
		decision       *v1alpha1.Decision
		simulateResult bool
		expectTimeout  bool
		expectError    bool
	}{
		{
			name: "successful API decision processing",
			decision: &v1alpha1.Decision{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-decision",
					Namespace: "default",
				},
				Spec: v1alpha1.DecisionSpec{
					Type:     v1alpha1.DecisionTypeNovaServer,
					Operator: "test-operator",
				},
			},
			simulateResult: true,
			expectTimeout:  false,
			expectError:    false,
		},
		{
			name: "timeout waiting for decision",
			decision: &v1alpha1.Decision{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-decision-timeout",
					Namespace: "default",
				},
				Spec: v1alpha1.DecisionSpec{
					Type:     v1alpha1.DecisionTypeNovaServer,
					Operator: "test-operator",
				},
			},
			simulateResult: false,
			expectTimeout:  true,
			expectError:    true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			client := fake.NewClientBuilder().
				WithScheme(scheme).
				WithStatusSubresource(&v1alpha1.Decision{}).
				Build()

			controller := &DecisionPipelineController{
				BasePipelineController: lib.BasePipelineController[lib.Pipeline[api.ExternalSchedulerRequest]]{
					Client: client,
				},
				DB:              testDB,
				Monitor:         lib.PipelineMonitor{},
				pendingRequests: make(map[string]*pendingRequest),
			}

			ctx := context.Background()
			if tt.expectTimeout {
				var cancel context.CancelFunc
				ctx, cancel = context.WithTimeout(context.Background(), 50*time.Millisecond)
				defer cancel()
			}

			if tt.simulateResult {
				go func() {
					time.Sleep(50 * time.Millisecond)
					decisionKey := tt.decision.Namespace + "/" + tt.decision.Name
					controller.mu.RLock()
					pending, exists := controller.pendingRequests[decisionKey]
					controller.mu.RUnlock()

					if exists {
						tt.decision.Status.Result = &v1alpha1.DecisionResult{
							OrderedHosts: []string{"test-host"},
							TargetHost:   func() *string { s := "test-host"; return &s }(),
						}
						select {
						case pending.responseChan <- tt.decision:
						case <-pending.cancelChan:
						}
					}
				}()
			}

			result, err := controller.ProcessNewDecisionFromAPI(ctx, tt.decision)

			if tt.expectError && err == nil {
				t.Error("Expected error but got none")
			}
			if !tt.expectError && err != nil {
				t.Errorf("Expected no error but got: %v", err)
			}

			if !tt.expectError {
				if result == nil {
					t.Error("Expected result but got nil")
				}
				if result != nil && result.Status.Result == nil {
					t.Error("Expected result status to be set")
				}
			}
		})
	}
}

func TestDecisionPipelineController_InitPipeline(t *testing.T) {
	dbEnv := testlibDB.SetupDBEnv(t)
	testDB := db.DB{DbMap: dbEnv.DbMap}
	defer testDB.Close()
	defer dbEnv.Close()

	controller := &DecisionPipelineController{
		DB:      testDB,
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
			pipeline, err := controller.InitPipeline(tt.steps)

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

func TestDecisionPipelineController_SetupWithManager(t *testing.T) {
	scheme := runtime.NewScheme()
	if err := v1alpha1.AddToScheme(scheme); err != nil {
		t.Fatalf("Failed to add v1alpha1 scheme: %v", err)
	}
	if err := knowledgev1alpha1.AddToScheme(scheme); err != nil {
		t.Fatalf("Failed to add knowledgev1alpha1 scheme: %v", err)
	}

	controller := &DecisionPipelineController{
		Conf: conf.Config{
			Operator: "test-operator",
		},
	}

	// This test verifies that SetupWithManager method exists and has the correct signature
	// We can't easily test the actual setup without a real manager, so we just verify the panic
	// is handled gracefully when called with nil
	defer func() {
		if r := recover(); r == nil {
			t.Error("Expected panic when calling SetupWithManager with nil manager")
		}
	}()

	err := controller.SetupWithManager(nil)
	if err != nil {
		t.Errorf("Unexpected error from SetupWithManager: %v", err)
	}
}
