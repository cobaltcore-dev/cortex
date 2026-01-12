// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package podgroupsets

import (
	"context"
	"testing"

	"github.com/cobaltcore-dev/cortex/api/delegation/podgroupsets"
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
		name                   string
		decision               *v1alpha1.Decision
		nodes                  []corev1.Node
		podGroupSet            *v1alpha1.PodGroupSet
		expectError            bool
		expectDecision         bool
		expectTargetPlacements int
	}{
		{
			name: "successful pgs decision processing",
			decision: &v1alpha1.Decision{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-decision",
				},
				Spec: v1alpha1.DecisionSpec{
					SchedulingDomain: v1alpha1.SchedulingDomainPodGroupSets,
					ResourceID:       "test-podGroupSet",
					PipelineRef: corev1.ObjectReference{
						Name: "podgroupsets-scheduler",
					},
					PodGroupSetRef: &corev1.ObjectReference{
						Name:      "test-podGroupSet",
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
			podGroupSet: &v1alpha1.PodGroupSet{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-podGroupSet",
					Namespace: "default",
				},
				Spec: v1alpha1.PodGroupSetSpec{
					PodGroups: []v1alpha1.PodGroup{
						{
							Name: "group1",
							Spec: v1alpha1.PodGroupSpec{
								Replicas: 1,
								PodSpec:  corev1.PodSpec{Containers: []corev1.Container{{Name: "c1"}}},
							},
						},
					},
				},
			},
			expectError:            false,
			expectDecision:         true,
			expectTargetPlacements: 1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			objects := []runtime.Object{tt.decision}
			for i := range tt.nodes {
				objects = append(objects, &tt.nodes[i])
			}
			if tt.podGroupSet != nil {
				objects = append(objects, tt.podGroupSet)
			}

			client := fake.NewClientBuilder().
				WithScheme(scheme).
				WithRuntimeObjects(objects...).
				WithStatusSubresource(&v1alpha1.Decision{}).
				Build()

			controller := &DecisionPipelineController{
				BasePipelineController: lib.BasePipelineController[lib.Pipeline[podgroupsets.PodGroupSetPipelineRequest]]{
					Pipelines: map[string]lib.Pipeline[podgroupsets.PodGroupSetPipelineRequest]{
						"podgroupsets-scheduler": createMockPodGroupSetPipeline(),
					},
				},
				Conf: conf.Config{
					SchedulingDomain: v1alpha1.SchedulingDomainPodGroupSets,
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

				if len(updatedDecision.Status.Result.TargetPlacements) != tt.expectTargetPlacements {
					t.Errorf("expected %d target placements, got %d", tt.expectTargetPlacements, len(updatedDecision.Status.Result.TargetPlacements))
				}
			}
		})
	}
}

func TestDecisionPipelineController_InitPipeline(t *testing.T) {
	controller := &DecisionPipelineController{
		Monitor: lib.PipelineMonitor{},
	}

	// This is a bit different from pods because we return a fixed pipeline
	pipeline, err := controller.InitPipeline(t.Context(), v1alpha1.Pipeline{})
	if err != nil {
		t.Errorf("expected no error, got: %v", err)
	}
	if pipeline == nil {
		t.Error("expected pipeline to be non-nil")
	}
}

func TestDecisionPipelineController_ProcessNewPodGroupSet(t *testing.T) {
	scheme := runtime.NewScheme()
	if err := v1alpha1.AddToScheme(scheme); err != nil {
		t.Fatalf("Failed to add scheduling scheme: %v", err)
	}
	if err := corev1.AddToScheme(scheme); err != nil {
		t.Fatalf("Failed to add corev1 scheme: %v", err)
	}

	tests := []struct {
		name                  string
		pgs                   *v1alpha1.PodGroupSet
		nodes                 []corev1.Node
		pipelineConfig        *v1alpha1.Pipeline
		expectError           bool
		expectDecisionCreated bool
		expectPodsCreated     int
	}{
		{
			name: "successful pgs processing with decision creation",
			pgs: &v1alpha1.PodGroupSet{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-podGroupSet",
					Namespace: "default",
				},
				Spec: v1alpha1.PodGroupSetSpec{
					PodGroups: []v1alpha1.PodGroup{
						{
							Name: "group1",
							Spec: v1alpha1.PodGroupSpec{
								Replicas: 1,
								PodSpec:  corev1.PodSpec{Containers: []corev1.Container{{Name: "c1"}}},
							},
						},
					},
				},
			},
			nodes: []corev1.Node{
				{
					ObjectMeta: metav1.ObjectMeta{Name: "node1"},
				},
			},
			pipelineConfig: &v1alpha1.Pipeline{
				ObjectMeta: metav1.ObjectMeta{
					Name: "podgroupsets-scheduler",
				},
				Spec: v1alpha1.PipelineSpec{
					Type:             v1alpha1.PipelineTypeGang,
					SchedulingDomain: v1alpha1.SchedulingDomainPodGroupSets,
					CreateDecisions:  true,
					Steps:            []v1alpha1.StepSpec{},
				},
			},
			expectError:           false,
			expectDecisionCreated: true,
			expectPodsCreated:     1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			objects := []runtime.Object{tt.pgs}
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
				BasePipelineController: lib.BasePipelineController[lib.Pipeline[podgroupsets.PodGroupSetPipelineRequest]]{
					Pipelines:       map[string]lib.Pipeline[podgroupsets.PodGroupSetPipelineRequest]{},
					PipelineConfigs: map[string]v1alpha1.Pipeline{},
				},
				Conf: conf.Config{
					SchedulingDomain: v1alpha1.SchedulingDomainPodGroupSets,
				},
				Monitor: lib.PipelineMonitor{},
			}
			controller.Client = client

			if tt.pipelineConfig != nil {
				controller.PipelineConfigs[tt.pipelineConfig.Name] = *tt.pipelineConfig
				controller.Pipelines[tt.pipelineConfig.Name] = createMockPodGroupSetPipeline()
			}

			err := controller.ProcessNewPodGroupSet(context.Background(), tt.pgs)

			if tt.expectError && err == nil {
				t.Error("expected error but got none")
				return
			}

			if !tt.expectError && err != nil {
				t.Errorf("expected no error, got: %v", err)
				return
			}

			if tt.expectDecisionCreated {
				var decisions v1alpha1.DecisionList
				err := client.List(context.Background(), &decisions)
				if err != nil {
					t.Errorf("Failed to list decisions: %v", err)
					return
				}
				if len(decisions.Items) != 1 {
					t.Errorf("expected 1 decision, got %d", len(decisions.Items))
				}
			}

			if tt.expectPodsCreated > 0 {
				var pods corev1.PodList
				err := client.List(context.Background(), &pods)
				if err != nil {
					t.Errorf("Failed to list pods: %v", err)
					return
				}
				if len(pods.Items) != tt.expectPodsCreated {
					t.Errorf("expected %d pods, got %d", tt.expectPodsCreated, len(pods.Items))
				}
			}
		})
	}
}

func createMockPodGroupSetPipeline() lib.Pipeline[podgroupsets.PodGroupSetPipelineRequest] {
	return &mockPodGroupSetPipeline{}
}

type mockPodGroupSetPipeline struct{}

func (m *mockPodGroupSetPipeline) Run(request podgroupsets.PodGroupSetPipelineRequest) (v1alpha1.DecisionResult, error) {
	if len(request.Nodes) == 0 {
		return v1alpha1.DecisionResult{}, nil
	}
	placements := make(map[string]string)
	for _, g := range request.PodGroupSet.Spec.PodGroups {
		placements[g.Name+"-0"] = request.Nodes[0].Name
	}
	return v1alpha1.DecisionResult{
		TargetPlacements: placements,
	}, nil
}
