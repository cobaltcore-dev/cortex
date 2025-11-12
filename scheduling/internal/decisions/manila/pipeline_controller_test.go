// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package manila

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	knowledgev1alpha1 "github.com/cobaltcore-dev/cortex/knowledge/api/v1alpha1"
	api "github.com/cobaltcore-dev/cortex/scheduling/api/delegation/manila"
	"github.com/cobaltcore-dev/cortex/scheduling/api/v1alpha1"
	"github.com/cobaltcore-dev/cortex/scheduling/internal/conf"
	"github.com/cobaltcore-dev/cortex/scheduling/internal/lib"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestDecisionPipelineController_Reconcile(t *testing.T) {
	scheme := runtime.NewScheme()
	if err := v1alpha1.AddToScheme(scheme); err != nil {
		t.Fatalf("Failed to add v1alpha1 scheme: %v", err)
	}
	if err := knowledgev1alpha1.AddToScheme(scheme); err != nil {
		t.Fatalf("Failed to add knowledgev1alpha1 scheme: %v", err)
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
			expectError:    false,
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
				pendingRequests: make(map[string]*pendingRequest),
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
		t.Fatalf("Failed to add scheme: %v", err)
	}

	client := fake.NewClientBuilder().
		WithScheme(scheme).
		WithStatusSubresource(&v1alpha1.Decision{}).
		Build()

	controller := &DecisionPipelineController{
		BasePipelineController: lib.BasePipelineController[lib.Pipeline[api.ExternalSchedulerRequest]]{
			Client: client,
		},
		Monitor:         lib.PipelineMonitor{},
		pendingRequests: make(map[string]*pendingRequest),
	}

	decision := &v1alpha1.Decision{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-decision",
			Namespace: "default",
		},
		Spec: v1alpha1.DecisionSpec{
			Type:     v1alpha1.DecisionTypeManilaShare,
			Operator: "test-operator",
		},
	}

	ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
	defer cancel()

	go func() {
		time.Sleep(100 * time.Millisecond)
		decisionKey := decision.Namespace + "/" + decision.Name
		controller.mu.RLock()
		pending, exists := controller.pendingRequests[decisionKey]
		controller.mu.RUnlock()

		if exists {
			decision.Status.Result = &v1alpha1.DecisionResult{
				OrderedHosts: []string{"test-host"},
				TargetHost:   func() *string { s := "test-host"; return &s }(),
			}
			select {
			case pending.responseChan <- decision:
			case <-pending.cancelChan:
			}
		}
	}()

	result, err := controller.ProcessNewDecisionFromAPI(ctx, decision)

	if err != nil {
		t.Errorf("Expected no error but got: %v", err)
	}

	if result == nil {
		t.Error("Expected result but got nil")
	}

	if result != nil && result.Status.Result == nil {
		t.Error("Expected result status to be set")
	}

	// Test timeout scenario
	t.Run("timeout scenario", func(t *testing.T) {
		decision2 := &v1alpha1.Decision{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "test-decision-timeout",
				Namespace: "default",
			},
			Spec: v1alpha1.DecisionSpec{
				Type:     v1alpha1.DecisionTypeManilaShare,
				Operator: "test-operator",
			},
		}

		ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
		defer cancel()

		result, err := controller.ProcessNewDecisionFromAPI(ctx, decision2)

		if err == nil {
			t.Error("Expected timeout error but got none")
		}
		if result != nil {
			t.Error("Expected nil result on timeout")
		}
	})
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
