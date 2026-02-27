// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package pods

import (
	"context"
	"testing"

	"github.com/cobaltcore-dev/cortex/api/external/pods"
	"github.com/cobaltcore-dev/cortex/api/v1alpha1"

	"github.com/cobaltcore-dev/cortex/internal/scheduling/lib"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

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
		name               string
		pod                *corev1.Pod
		nodes              []corev1.Node
		pipelineConfig     *v1alpha1.Pipeline
		expectError        bool
		expectNodeAssigned bool
		expectTargetHost   string
	}{
		{
			name: "successful pod processing",
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
					CreateDecisions:  false,
					Filters:          []v1alpha1.FilterSpec{},
					Weighers:         []v1alpha1.WeigherSpec{},
				},
			},
			expectError:        false,
			expectNodeAssigned: true,
			expectTargetHost:   "node1",
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
			nodes:              []corev1.Node{},
			pipelineConfig:     nil,
			expectError:        true,
			expectNodeAssigned: false,
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
					Filters:          []v1alpha1.FilterSpec{},
					Weighers:         []v1alpha1.WeigherSpec{},
				},
			},
			expectError:        true,
			expectNodeAssigned: false,
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

			controller := &FilterWeigherPipelineController{
				BasePipelineController: lib.BasePipelineController[lib.FilterWeigherPipeline[pods.PodPipelineRequest]]{
					Pipelines:       map[string]lib.FilterWeigherPipeline[pods.PodPipelineRequest]{},
					PipelineConfigs: map[string]v1alpha1.Pipeline{},
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

func (m *mockPodPipeline) Run(request pods.PodPipelineRequest) (lib.FilterWeigherPipelineResult, error) {
	if len(request.Nodes) == 0 {
		return lib.FilterWeigherPipelineResult{OrderedHosts: []string{}}, nil
	}

	// Return the first node as the target host
	targetHost := request.Nodes[0].Name
	return lib.FilterWeigherPipelineResult{
		OrderedHosts: []string{targetHost},
	}, nil
}
