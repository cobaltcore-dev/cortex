// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package pods

import (
	"context"
	"testing"
	"time"

	"github.com/cobaltcore-dev/cortex/api/external/pods"
	"github.com/cobaltcore-dev/cortex/api/v1alpha1"

	"github.com/cobaltcore-dev/cortex/internal/scheduling/lib"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func TestFilterWeigherPipelineController_Reconcile(t *testing.T) {
	scheme := runtime.NewScheme()
	if err := v1alpha1.AddToScheme(scheme); err != nil {
		t.Fatalf("Failed to add scheduling scheme: %v", err)
	}
	if err := corev1.AddToScheme(scheme); err != nil {
		t.Fatalf("Failed to add corev1 scheme: %v", err)
	}

	tests := []struct {
		name             string
		decision         *v1alpha1.Decision
		nodes            []corev1.Node
		pod              *corev1.Pod
		expectError      bool
		expectDecision   bool
		expectTargetHost string
	}{
		{
			name: "successful pod decision processing",
			decision: &v1alpha1.Decision{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-decision",
				},
				Spec: v1alpha1.DecisionSpec{
					SchedulingDomain: v1alpha1.SchedulingDomainPods,
					ResourceID:       "test-pod",
					PipelineRef: corev1.ObjectReference{
						Name: "pods-scheduler",
					},
					PodRef: &corev1.ObjectReference{
						Name:      "test-pod",
						Namespace: "default",
					},
				},
			},
			nodes: []corev1.Node{
				{
					ObjectMeta: metav1.ObjectMeta{Name: "node1"},
				},
				{
					ObjectMeta: metav1.ObjectMeta{Name: "node2"},
				},
			},
			pod: &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-pod",
					Namespace: "default",
				},
				Spec: corev1.PodSpec{
					SchedulerName: "",
				},
			},
			expectError:      false,
			expectDecision:   true,
			expectTargetHost: "node1", // NoopFilter returns first node
		},
		{
			name: "no nodes available",
			decision: &v1alpha1.Decision{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-decision-no-nodes",
				},
				Spec: v1alpha1.DecisionSpec{
					SchedulingDomain: v1alpha1.SchedulingDomainPods,
					ResourceID:       "test-pod",
					PipelineRef: corev1.ObjectReference{
						Name: "pods-scheduler",
					},
					PodRef: &corev1.ObjectReference{
						Name:      "test-pod",
						Namespace: "default",
					},
				},
			},
			nodes:          []corev1.Node{},
			expectError:    true,
			expectDecision: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			objects := []runtime.Object{tt.decision}
			for i := range tt.nodes {
				objects = append(objects, &tt.nodes[i])
			}
			if tt.pod != nil {
				objects = append(objects, tt.pod)
			}

			client := fake.NewClientBuilder().
				WithScheme(scheme).
				WithRuntimeObjects(objects...).
				WithStatusSubresource(&v1alpha1.Decision{}).
				Build()

			controller := &FilterWeigherPipelineController{
				BasePipelineController: lib.BasePipelineController[lib.FilterWeigherPipeline[pods.PodPipelineRequest]]{
					Pipelines: map[string]lib.FilterWeigherPipeline[pods.PodPipelineRequest]{
						"pods-scheduler": createMockPodPipeline(),
					},
				},
				Monitor: lib.FilterWeigherPipelineMonitor{},
			}
			controller.Client = client

			req := ctrl.Request{
				NamespacedName: types.NamespacedName{
					Name: tt.decision.Name,
				},
			}

			result, err := controller.Reconcile(context.Background(), req)

			if tt.expectError && err == nil {
				t.Error("expected error but got none")
				return
			}

			if !tt.expectError && err != nil {
				t.Errorf("expected no error, got: %v", err)
				return
			}

			if result.RequeueAfter > 0 {
				t.Errorf("unexpected requeue: %v", result.RequeueAfter)
			}

			// Verify decision status if expected
			if tt.expectDecision {
				var updatedDecision v1alpha1.Decision
				err := client.Get(context.Background(), req.NamespacedName, &updatedDecision)
				if err != nil {
					t.Errorf("Failed to get updated decision: %v", err)
					return
				}

				if updatedDecision.Status.Result == nil {
					t.Error("expected decision result to be set")
					return
				}

				if updatedDecision.Status.Result.TargetHost == nil {
					t.Error("expected target host to be set")
					return
				}

				if *updatedDecision.Status.Result.TargetHost != tt.expectTargetHost {
					t.Errorf("expected target host %q, got %q", tt.expectTargetHost, *updatedDecision.Status.Result.TargetHost)
				}
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
		expectUnknownFilter    bool
		expectUnknownWeigher   bool
	}{
		{
			name:                   "empty steps",
			filters:                []v1alpha1.FilterSpec{},
			weighers:               []v1alpha1.WeigherSpec{},
			expectNonCriticalError: false,
			expectCriticalError:    false,
			expectUnknownFilter:    false,
			expectUnknownWeigher:   false,
		},
		{
			name: "noop step",
			filters: []v1alpha1.FilterSpec{
				{
					Name: "noop",
				},
			},
			expectNonCriticalError: false,
			expectCriticalError:    false,
			expectUnknownFilter:    false,
			expectUnknownWeigher:   false,
		},
		{
			name: "unsupported step",
			filters: []v1alpha1.FilterSpec{
				{
					Name: "unsupported",
				},
			},
			expectNonCriticalError: false,
			expectCriticalError:    false,
			expectUnknownFilter:    true,
			expectUnknownWeigher:   false,
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

			if tt.expectCriticalError && len(initResult.FilterErrors) == 0 {
				t.Error("expected critical error but got none")
			}
			if !tt.expectCriticalError && len(initResult.FilterErrors) > 0 {
				t.Errorf("unexpected critical error: %v", initResult.FilterErrors)
			}

			if tt.expectNonCriticalError && len(initResult.WeigherErrors) == 0 {
				t.Error("expected non-critical error but got none")
			}
			if !tt.expectNonCriticalError && len(initResult.WeigherErrors) > 0 {
				t.Errorf("unexpected non-critical error: %v", initResult.WeigherErrors)
			}
		})
	}
}

func TestFilterWeigherPipelineController_ProcessNewPod(t *testing.T) {
	scheme := runtime.NewScheme()
	if err := v1alpha1.AddToScheme(scheme); err != nil {
		t.Fatalf("Failed to add scheduling scheme: %v", err)
	}
	if err := corev1.AddToScheme(scheme); err != nil {
		t.Fatalf("Failed to add corev1 scheme: %v", err)
	}

	tests := []struct {
		name                 string
		pod                  *corev1.Pod
		nodes                []corev1.Node
		pipelineConfig       *v1alpha1.Pipeline
		createHistory        bool
		expectError          bool
		expectHistoryCreated bool
		expectNodeAssigned   bool
		expectTargetHost     string
	}{
		{
			name: "successful pod processing with decision creation",
			pod: &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-pod",
					Namespace: "default",
				},
				Spec: corev1.PodSpec{
					SchedulerName: "",
				},
			},
			nodes: []corev1.Node{
				{
					ObjectMeta: metav1.ObjectMeta{Name: "node1"},
				},
				{
					ObjectMeta: metav1.ObjectMeta{Name: "node2"},
				},
			},
			pipelineConfig: &v1alpha1.Pipeline{
				ObjectMeta: metav1.ObjectMeta{
					Name: "pods-scheduler",
				},
				Spec: v1alpha1.PipelineSpec{
					Type:             v1alpha1.PipelineTypeFilterWeigher,
					SchedulingDomain: v1alpha1.SchedulingDomainPods,
					Filters:          []v1alpha1.FilterSpec{},
					Weighers:         []v1alpha1.WeigherSpec{},
				},
			},
			createHistory:        true,
			expectError:          false,
			expectHistoryCreated: true,
			expectNodeAssigned:   true,
			expectTargetHost:     "node1",
		},
		{
			name: "successful pod processing without decision creation",
			pod: &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-pod-no-decision",
					Namespace: "default",
				},
				Spec: corev1.PodSpec{
					SchedulerName: "",
				},
			},
			nodes: []corev1.Node{
				{
					ObjectMeta: metav1.ObjectMeta{Name: "node1"},
				},
			},
			pipelineConfig: &v1alpha1.Pipeline{
				ObjectMeta: metav1.ObjectMeta{
					Name: "pods-scheduler",
				},
				Spec: v1alpha1.PipelineSpec{
					Type:             v1alpha1.PipelineTypeFilterWeigher,
					SchedulingDomain: v1alpha1.SchedulingDomainPods,
					Filters:          []v1alpha1.FilterSpec{},
					Weighers:         []v1alpha1.WeigherSpec{},
				},
			},
			createHistory:        false,
			expectError:          false,
			expectHistoryCreated: false,
			expectNodeAssigned:   true,
			expectTargetHost:     "node1",
		},
		{
			name: "pipeline not configured",
			pod: &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-pod-no-pipeline",
					Namespace: "default",
				},
				Spec: corev1.PodSpec{
					SchedulerName: "",
				},
			},
			nodes:                []corev1.Node{},
			pipelineConfig:       nil,
			expectError:          true,
			expectHistoryCreated: false,
			expectNodeAssigned:   false,
		},
		{
			name: "no nodes available",
			pod: &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-pod-no-nodes",
					Namespace: "default",
				},
				Spec: corev1.PodSpec{
					SchedulerName: "",
				},
			},
			nodes: []corev1.Node{},
			pipelineConfig: &v1alpha1.Pipeline{
				ObjectMeta: metav1.ObjectMeta{
					Name: "pods-scheduler",
				},
				Spec: v1alpha1.PipelineSpec{
					Type:             v1alpha1.PipelineTypeFilterWeigher,
					SchedulingDomain: v1alpha1.SchedulingDomainPods,
					Filters:          []v1alpha1.FilterSpec{},
					Weighers:         []v1alpha1.WeigherSpec{},
				},
			},
			createHistory:        true,
			expectError:          true,
			expectHistoryCreated: true, // Decision is created but processing fails
			expectNodeAssigned:   false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			objects := []runtime.Object{tt.pod}
			for i := range tt.nodes {
				objects = append(objects, &tt.nodes[i])
			}
			if tt.pipelineConfig != nil {
				objects = append(objects, tt.pipelineConfig)
			}

			client := fake.NewClientBuilder().
				WithScheme(scheme).
				WithRuntimeObjects(objects...).
				WithStatusSubresource(&v1alpha1.Decision{}, &v1alpha1.History{}).
				Build()

			controller := &FilterWeigherPipelineController{
				BasePipelineController: lib.BasePipelineController[lib.FilterWeigherPipeline[pods.PodPipelineRequest]]{
					Pipelines:       map[string]lib.FilterWeigherPipeline[pods.PodPipelineRequest]{},
					PipelineConfigs: map[string]v1alpha1.Pipeline{},
					HistoryManager:  lib.HistoryClient{Client: client},
				},
				Monitor: lib.FilterWeigherPipelineMonitor{},
			}
			controller.Client = client

			if tt.pipelineConfig != nil {
				controller.PipelineConfigs[tt.pipelineConfig.Name] = *tt.pipelineConfig
				controller.Pipelines[tt.pipelineConfig.Name] = createMockPodPipeline()
			}

			err := controller.ProcessNewPod(context.Background(), tt.pod)

			if tt.expectError && err == nil {
				t.Error("expected error but got none")
				return
			}

			if !tt.expectError && err != nil {
				t.Errorf("expected no error, got: %v", err)
				return
			}

			// Check if history CRD was created when expected
			if tt.expectHistoryCreated {
				var histories v1alpha1.HistoryList
				deadline := time.Now().Add(2 * time.Second)
				for {
					if err := client.List(context.Background(), &histories); err != nil {
						t.Fatalf("Failed to list histories: %v", err)
					}
					if len(histories.Items) > 0 {
						break
					}
					if time.Now().After(deadline) {
						t.Fatal("timed out waiting for history CRD to be created")
					}
					time.Sleep(5 * time.Millisecond)
				}
			} else {
				var histories v1alpha1.HistoryList
				if err := client.List(context.Background(), &histories); err != nil {
					t.Fatalf("Failed to list histories: %v", err)
				}
				if len(histories.Items) != 0 {
					t.Error("Expected no history CRD but found one")
				}
			}

			// Check if node was assigned (if expected)
			if tt.expectNodeAssigned {
				var binding corev1.Binding
				err := client.Get(context.Background(), types.NamespacedName{
					Name:      tt.pod.Name,
					Namespace: tt.pod.Namespace,
				}, &binding)
				if err != nil {
					t.Errorf("Failed to get binding: %v", err)
					return
				}

				if binding.Target.Kind != "Node" {
					t.Errorf("expected binding target kind Node, got %q", binding.Target.Kind)
				}
				if binding.Target.Name != tt.expectTargetHost {
					t.Errorf("expected binding target name %q, got %q", tt.expectTargetHost, binding.Target.Name)
				}
			}
		})
	}
}

// Helper function to create a mock pipeline that works with the pod types
func createMockPodPipeline() lib.FilterWeigherPipeline[pods.PodPipelineRequest] {
	return &mockPodPipeline{}
}

type mockPodPipeline struct{}

func (m *mockPodPipeline) Run(request pods.PodPipelineRequest) (v1alpha1.DecisionResult, error) {
	if len(request.Nodes) == 0 {
		return v1alpha1.DecisionResult{}, nil
	}

	// Return the first node as the target host
	targetHost := request.Nodes[0].Name
	return v1alpha1.DecisionResult{
		TargetHost: &targetHost,
	}, nil
}
