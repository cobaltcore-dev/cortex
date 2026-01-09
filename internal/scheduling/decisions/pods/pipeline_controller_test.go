// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package pods

import (
	"context"
	"testing"

	"github.com/cobaltcore-dev/cortex/api/delegation/pods"
	"github.com/cobaltcore-dev/cortex/api/v1alpha1"

	"github.com/cobaltcore-dev/cortex/internal/scheduling/lib"
	"github.com/cobaltcore-dev/cortex/pkg/conf"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func TestDecisionPipelineController_Reconcile(t *testing.T) {
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

			controller := &DecisionPipelineController{
				BasePipelineController: lib.BasePipelineController[lib.Pipeline[pods.PodPipelineRequest]]{
					Pipelines: map[string]lib.Pipeline[pods.PodPipelineRequest]{
						"pods-scheduler": createMockPodPipeline(),
					},
				},
				Conf: conf.Config{
					SchedulingDomain: v1alpha1.SchedulingDomainPods,
				},
				Monitor: lib.PipelineMonitor{},
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

				if updatedDecision.Status.Took.Duration <= 0 {
					t.Error("expected took duration to be positive")
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
		steps       []v1alpha1.StepSpec
		expectError bool
	}{
		{
			name:        "empty steps",
			steps:       []v1alpha1.StepSpec{},
			expectError: false,
		},
		{
			name: "noop step",
			steps: []v1alpha1.StepSpec{
				{
					Impl: "noop",
					Type: v1alpha1.StepTypeFilter,
				},
			},
			expectError: false,
		},
		{
			name: "unsupported step",
			steps: []v1alpha1.StepSpec{
				{
					Impl: "unsupported",
					Type: v1alpha1.StepTypeFilter,
				},
			},
			expectError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			pipeline, err := controller.InitPipeline(t.Context(), v1alpha1.Pipeline{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-pipeline",
				},
				Spec: v1alpha1.PipelineSpec{
					Steps: tt.steps,
				},
			})

			if tt.expectError && err == nil {
				t.Error("expected error but got none")
				return
			}

			if !tt.expectError && err != nil {
				t.Errorf("expected no error, got: %v", err)
				return
			}

			if !tt.expectError && pipeline == nil {
				t.Error("expected pipeline to be non-nil")
			}
		})
	}
}

func TestDecisionPipelineController_ProcessNewPod(t *testing.T) {
	scheme := runtime.NewScheme()
	if err := v1alpha1.AddToScheme(scheme); err != nil {
		t.Fatalf("Failed to add scheduling scheme: %v", err)
	}
	if err := corev1.AddToScheme(scheme); err != nil {
		t.Fatalf("Failed to add corev1 scheme: %v", err)
	}

	tests := []struct {
		name                  string
		pod                   *corev1.Pod
		nodes                 []corev1.Node
		pipelineConfig        *v1alpha1.Pipeline
		createDecisions       bool
		expectError           bool
		expectDecisionCreated bool
		expectNodeAssigned    bool
		expectTargetHost      string
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
					CreateDecisions:  true,
					Steps:            []v1alpha1.StepSpec{},
				},
			},
			createDecisions:       true,
			expectError:           false,
			expectDecisionCreated: true,
			expectNodeAssigned:    true,
			expectTargetHost:      "node1",
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
					CreateDecisions:  false,
					Steps:            []v1alpha1.StepSpec{},
				},
			},
			createDecisions:       false,
			expectError:           false,
			expectDecisionCreated: false,
			expectNodeAssigned:    true,
			expectTargetHost:      "node1",
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
			nodes:                 []corev1.Node{},
			pipelineConfig:        nil,
			expectError:           true,
			expectDecisionCreated: false,
			expectNodeAssigned:    false,
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
					CreateDecisions:  true,
					Steps:            []v1alpha1.StepSpec{},
				},
			},
			createDecisions:       true,
			expectError:           true,
			expectDecisionCreated: true, // Decision is created but processing fails
			expectNodeAssigned:    false,
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
				WithStatusSubresource(&v1alpha1.Decision{}).
				Build()

			controller := &DecisionPipelineController{
				BasePipelineController: lib.BasePipelineController[lib.Pipeline[pods.PodPipelineRequest]]{
					Pipelines:       map[string]lib.Pipeline[pods.PodPipelineRequest]{},
					PipelineConfigs: map[string]v1alpha1.Pipeline{},
				},
				Conf: conf.Config{
					SchedulingDomain: v1alpha1.SchedulingDomainPods,
				},
				Monitor: lib.PipelineMonitor{},
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
					if decision.Spec.PodRef != nil &&
						decision.Spec.PodRef.Name == tt.pod.Name &&
						decision.Spec.PodRef.Namespace == tt.pod.Namespace {
						found = true

						// Verify decision properties
						if decision.Spec.SchedulingDomain != v1alpha1.SchedulingDomainPods {
							t.Errorf("expected scheduling domain %q, got %q", v1alpha1.SchedulingDomainPods, decision.Spec.SchedulingDomain)
						}
						if decision.Spec.ResourceID != tt.pod.Name {
							t.Errorf("expected resource ID %q, got %q", tt.pod.Name, decision.Spec.ResourceID)
						}
						if decision.Spec.PipelineRef.Name != "pods-scheduler" {
							t.Errorf("expected pipeline ref %q, got %q", "pods-scheduler", decision.Spec.PipelineRef.Name)
						}

						// Check if result was set (only for successful cases)
						if !tt.expectError && tt.expectTargetHost != "" {
							if decision.Status.Result == nil {
								t.Error("expected decision result to be set")
								return
							}
							if decision.Status.Result.TargetHost == nil {
								t.Error("expected target host to be set")
								return
							}
							if *decision.Status.Result.TargetHost != tt.expectTargetHost {
								t.Errorf("expected target host %q, got %q", tt.expectTargetHost, *decision.Status.Result.TargetHost)
							}
						}
						break
					}
				}

				if !found {
					t.Error("expected decision to be created but was not found")
				}
			} else {
				// Check that no decisions were created
				var decisions v1alpha1.DecisionList
				err := client.List(context.Background(), &decisions)
				if err != nil {
					t.Errorf("Failed to list decisions: %v", err)
					return
				}

				for _, decision := range decisions.Items {
					if decision.Spec.PodRef != nil &&
						decision.Spec.PodRef.Name == tt.pod.Name &&
						decision.Spec.PodRef.Namespace == tt.pod.Namespace {
						t.Error("expected no decision to be created but found one")
						break
					}
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
func createMockPodPipeline() lib.Pipeline[pods.PodPipelineRequest] {
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
