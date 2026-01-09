// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package lib

import (
	"context"
	"testing"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/event"

	"github.com/cobaltcore-dev/cortex/api/v1alpha1"
)

// Mock pipeline type for testing
type mockPipeline struct {
	name string
}

// Mock PipelineInitializer for testing
type mockPipelineInitializer struct {
	pipelineType     v1alpha1.PipelineType
	initPipelineFunc func(ctx context.Context, p v1alpha1.Pipeline) (mockPipeline, error)
}

func (m *mockPipelineInitializer) InitPipeline(ctx context.Context, p v1alpha1.Pipeline) (mockPipeline, error) {
	if m.initPipelineFunc != nil {
		return m.initPipelineFunc(ctx, p)
	}
	return mockPipeline{name: p.Name}, nil
}

func (m *mockPipelineInitializer) PipelineType() v1alpha1.PipelineType {
	return m.pipelineType
}

func TestBasePipelineController_InitAllPipelines(t *testing.T) {
	scheme := runtime.NewScheme()
	if err := v1alpha1.AddToScheme(scheme); err != nil {
		t.Fatalf("Failed to add v1alpha1 scheme: %v", err)
	}

	tests := []struct {
		name              string
		existingPipelines []v1alpha1.Pipeline
		schedulingDomain  v1alpha1.SchedulingDomain
		pipelineType      v1alpha1.PipelineType
		expectedCount     int
		expectError       bool
	}{
		{
			name:              "no existing pipelines",
			existingPipelines: []v1alpha1.Pipeline{},
			schedulingDomain:  v1alpha1.SchedulingDomainNova,
			pipelineType:      v1alpha1.PipelineTypeFilterWeigher,
			expectedCount:     0,
			expectError:       false,
		},
		{
			name: "one matching pipeline",
			existingPipelines: []v1alpha1.Pipeline{
				{
					ObjectMeta: metav1.ObjectMeta{
						Name: "test-pipeline",
					},
					Spec: v1alpha1.PipelineSpec{
						SchedulingDomain: v1alpha1.SchedulingDomainNova,
						Type:             v1alpha1.PipelineTypeFilterWeigher,
						Steps:            []v1alpha1.StepSpec{},
					},
				},
			},
			schedulingDomain: v1alpha1.SchedulingDomainNova,
			pipelineType:     v1alpha1.PipelineTypeFilterWeigher,
			expectedCount:    1,
			expectError:      false,
		},
		{
			name: "multiple pipelines, only some matching",
			existingPipelines: []v1alpha1.Pipeline{
				{
					ObjectMeta: metav1.ObjectMeta{
						Name: "matching-pipeline-1",
					},
					Spec: v1alpha1.PipelineSpec{
						SchedulingDomain: v1alpha1.SchedulingDomainNova,
						Type:             v1alpha1.PipelineTypeFilterWeigher,
						Steps:            []v1alpha1.StepSpec{},
					},
				},
				{
					ObjectMeta: metav1.ObjectMeta{
						Name: "different-domain-pipeline",
					},
					Spec: v1alpha1.PipelineSpec{
						SchedulingDomain: v1alpha1.SchedulingDomainCinder,
						Type:             v1alpha1.PipelineTypeFilterWeigher,
						Steps:            []v1alpha1.StepSpec{},
					},
				},
				{
					ObjectMeta: metav1.ObjectMeta{
						Name: "different-type-pipeline",
					},
					Spec: v1alpha1.PipelineSpec{
						SchedulingDomain: v1alpha1.SchedulingDomainNova,
						Type:             v1alpha1.PipelineTypeDescheduler,
						Steps:            []v1alpha1.StepSpec{},
					},
				},
				{
					ObjectMeta: metav1.ObjectMeta{
						Name: "matching-pipeline-2",
					},
					Spec: v1alpha1.PipelineSpec{
						SchedulingDomain: v1alpha1.SchedulingDomainNova,
						Type:             v1alpha1.PipelineTypeFilterWeigher,
						Steps:            []v1alpha1.StepSpec{},
					},
				},
			},
			schedulingDomain: v1alpha1.SchedulingDomainNova,
			pipelineType:     v1alpha1.PipelineTypeFilterWeigher,
			expectedCount:    2,
			expectError:      false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			objects := make([]client.Object, len(tt.existingPipelines))
			for i := range tt.existingPipelines {
				objects[i] = &tt.existingPipelines[i]
			}

			fakeClient := fake.NewClientBuilder().
				WithScheme(scheme).
				WithObjects(objects...).
				WithStatusSubresource(&v1alpha1.Pipeline{}).
				Build()

			controller := &BasePipelineController[mockPipeline]{
				Client:           fakeClient,
				SchedulingDomain: tt.schedulingDomain,
				Initializer: &mockPipelineInitializer{
					pipelineType: tt.pipelineType,
				},
			}

			err := controller.InitAllPipelines(context.Background())

			if tt.expectError && err == nil {
				t.Error("Expected error but got none")
			}
			if !tt.expectError && err != nil {
				t.Errorf("Expected no error but got: %v", err)
			}

			if len(controller.Pipelines) != tt.expectedCount {
				t.Errorf("Expected %d pipelines, got %d", tt.expectedCount, len(controller.Pipelines))
			}

			if len(controller.PipelineConfigs) != tt.expectedCount {
				t.Errorf("Expected %d pipeline configs, got %d", tt.expectedCount, len(controller.PipelineConfigs))
			}
		})
	}
}

func TestBasePipelineController_handlePipelineChange(t *testing.T) {
	scheme := runtime.NewScheme()
	if err := v1alpha1.AddToScheme(scheme); err != nil {
		t.Fatalf("Failed to add v1alpha1 scheme: %v", err)
	}

	tests := []struct {
		name              string
		pipeline          *v1alpha1.Pipeline
		knowledges        []v1alpha1.Knowledge
		schedulingDomain  v1alpha1.SchedulingDomain
		initPipelineError bool
		expectReady       bool
		expectInMap       bool
		expectCondition   string
	}{
		{
			name: "pipeline with all steps ready",
			pipeline: &v1alpha1.Pipeline{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-pipeline",
				},
				Spec: v1alpha1.PipelineSpec{
					SchedulingDomain: v1alpha1.SchedulingDomainNova,
					Type:             v1alpha1.PipelineTypeFilterWeigher,
					Steps: []v1alpha1.StepSpec{
						{
							Type:      v1alpha1.StepTypeFilter,
							Impl:      "test-filter",
							Mandatory: true,
							Knowledges: []corev1.ObjectReference{
								{Name: "knowledge-1", Namespace: "default"},
							},
						},
					},
				},
			},
			knowledges: []v1alpha1.Knowledge{
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "knowledge-1",
						Namespace: "default",
					},
					Spec: v1alpha1.KnowledgeSpec{
						SchedulingDomain: v1alpha1.SchedulingDomainNova,
					},
					Status: v1alpha1.KnowledgeStatus{
						RawLength: 10,
					},
				},
			},
			schedulingDomain: v1alpha1.SchedulingDomainNova,
			expectReady:      true,
			expectInMap:      true,
		},
		{
			name: "pipeline with mandatory step not ready",
			pipeline: &v1alpha1.Pipeline{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-pipeline-not-ready",
				},
				Spec: v1alpha1.PipelineSpec{
					SchedulingDomain: v1alpha1.SchedulingDomainNova,
					Type:             v1alpha1.PipelineTypeFilterWeigher,
					Steps: []v1alpha1.StepSpec{
						{
							Type:      v1alpha1.StepTypeFilter,
							Impl:      "test-filter",
							Mandatory: true,
							Knowledges: []corev1.ObjectReference{
								{Name: "missing-knowledge", Namespace: "default"},
							},
						},
					},
				},
			},
			knowledges:       []v1alpha1.Knowledge{},
			schedulingDomain: v1alpha1.SchedulingDomainNova,
			expectReady:      false,
			expectInMap:      false,
			expectCondition:  v1alpha1.PipelineConditionError,
		},
		{
			name: "pipeline with optional step not ready",
			pipeline: &v1alpha1.Pipeline{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-pipeline-optional",
				},
				Spec: v1alpha1.PipelineSpec{
					SchedulingDomain: v1alpha1.SchedulingDomainNova,
					Type:             v1alpha1.PipelineTypeFilterWeigher,
					Steps: []v1alpha1.StepSpec{
						{
							Type:      v1alpha1.StepTypeFilter,
							Impl:      "test-filter",
							Mandatory: false,
							Knowledges: []corev1.ObjectReference{
								{Name: "missing-knowledge", Namespace: "default"},
							},
						},
					},
				},
			},
			knowledges:       []v1alpha1.Knowledge{},
			schedulingDomain: v1alpha1.SchedulingDomainNova,
			expectReady:      true,
			expectInMap:      true,
		},
		{
			name: "pipeline init fails",
			pipeline: &v1alpha1.Pipeline{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-pipeline-init-fail",
				},
				Spec: v1alpha1.PipelineSpec{
					SchedulingDomain: v1alpha1.SchedulingDomainNova,
					Type:             v1alpha1.PipelineTypeFilterWeigher,
					Steps:            []v1alpha1.StepSpec{},
				},
			},
			knowledges:        []v1alpha1.Knowledge{},
			schedulingDomain:  v1alpha1.SchedulingDomainNova,
			initPipelineError: true,
			expectReady:       false,
			expectInMap:       false,
			expectCondition:   v1alpha1.PipelineConditionError,
		},
		{
			name: "pipeline with different scheduling domain",
			pipeline: &v1alpha1.Pipeline{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-pipeline-different-domain",
				},
				Spec: v1alpha1.PipelineSpec{
					SchedulingDomain: v1alpha1.SchedulingDomainCinder,
					Type:             v1alpha1.PipelineTypeFilterWeigher,
					Steps:            []v1alpha1.StepSpec{},
				},
			},
			knowledges:       []v1alpha1.Knowledge{},
			schedulingDomain: v1alpha1.SchedulingDomainNova,
			expectReady:      false,
			expectInMap:      false,
		},
		{
			name: "pipeline with knowledge in error state",
			pipeline: &v1alpha1.Pipeline{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-pipeline-knowledge-error",
				},
				Spec: v1alpha1.PipelineSpec{
					SchedulingDomain: v1alpha1.SchedulingDomainNova,
					Type:             v1alpha1.PipelineTypeFilterWeigher,
					Steps: []v1alpha1.StepSpec{
						{
							Type:      v1alpha1.StepTypeFilter,
							Impl:      "test-filter",
							Mandatory: true,
							Knowledges: []corev1.ObjectReference{
								{Name: "error-knowledge", Namespace: "default"},
							},
						},
					},
				},
			},
			knowledges: []v1alpha1.Knowledge{
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "error-knowledge",
						Namespace: "default",
					},
					Spec: v1alpha1.KnowledgeSpec{
						SchedulingDomain: v1alpha1.SchedulingDomainNova,
					},
					Status: v1alpha1.KnowledgeStatus{
						RawLength: 10,
						Conditions: []metav1.Condition{
							{
								Type:   v1alpha1.KnowledgeConditionError,
								Status: metav1.ConditionTrue,
							},
						},
					},
				},
			},
			schedulingDomain: v1alpha1.SchedulingDomainNova,
			expectReady:      false,
			expectInMap:      false,
			expectCondition:  v1alpha1.PipelineConditionError,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			objects := []client.Object{tt.pipeline}
			for i := range tt.knowledges {
				objects = append(objects, &tt.knowledges[i])
			}

			fakeClient := fake.NewClientBuilder().
				WithScheme(scheme).
				WithObjects(objects...).
				WithStatusSubresource(&v1alpha1.Pipeline{}, &v1alpha1.Knowledge{}).
				Build()

			initializer := &mockPipelineInitializer{
				pipelineType: v1alpha1.PipelineTypeFilterWeigher,
			}

			if tt.initPipelineError {
				initializer.initPipelineFunc = func(ctx context.Context, p v1alpha1.Pipeline) (mockPipeline, error) {
					return mockPipeline{}, context.Canceled
				}
			}

			controller := &BasePipelineController[mockPipeline]{
				Client:           fakeClient,
				SchedulingDomain: tt.schedulingDomain,
				Initializer:      initializer,
				Pipelines:        make(map[string]mockPipeline),
				PipelineConfigs:  make(map[string]v1alpha1.Pipeline),
			}

			controller.handlePipelineChange(context.Background(), tt.pipeline, nil)

			// Check if pipeline is in map
			_, inMap := controller.Pipelines[tt.pipeline.Name]
			if inMap != tt.expectInMap {
				t.Errorf("Expected pipeline in map: %v, got: %v", tt.expectInMap, inMap)
			}

			// Get updated pipeline status
			var updatedPipeline v1alpha1.Pipeline
			err := fakeClient.Get(context.Background(), client.ObjectKey{Name: tt.pipeline.Name}, &updatedPipeline)
			if err != nil {
				t.Fatalf("Failed to get updated pipeline: %v", err)
			}

			// Check ready status
			if updatedPipeline.Status.Ready != tt.expectReady {
				t.Errorf("Expected ready status: %v, got: %v", tt.expectReady, updatedPipeline.Status.Ready)
			}

			// Check condition if specified
			if tt.expectCondition != "" {
				hasCondition := meta.IsStatusConditionTrue(updatedPipeline.Status.Conditions, tt.expectCondition)
				if !hasCondition {
					t.Errorf("Expected condition %s to be true", tt.expectCondition)
				}
			}
		})
	}
}

func TestBasePipelineController_HandlePipelineCreated(t *testing.T) {
	scheme := runtime.NewScheme()
	if err := v1alpha1.AddToScheme(scheme); err != nil {
		t.Fatalf("Failed to add v1alpha1 scheme: %v", err)
	}

	pipeline := &v1alpha1.Pipeline{
		ObjectMeta: metav1.ObjectMeta{
			Name: "test-pipeline",
		},
		Spec: v1alpha1.PipelineSpec{
			SchedulingDomain: v1alpha1.SchedulingDomainNova,
			Type:             v1alpha1.PipelineTypeFilterWeigher,
			Steps:            []v1alpha1.StepSpec{},
		},
	}

	fakeClient := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(pipeline).
		WithStatusSubresource(&v1alpha1.Pipeline{}).
		Build()

	controller := &BasePipelineController[mockPipeline]{
		Client:           fakeClient,
		SchedulingDomain: v1alpha1.SchedulingDomainNova,
		Initializer: &mockPipelineInitializer{
			pipelineType: v1alpha1.PipelineTypeFilterWeigher,
		},
		Pipelines:       make(map[string]mockPipeline),
		PipelineConfigs: make(map[string]v1alpha1.Pipeline),
	}

	evt := event.CreateEvent{
		Object: pipeline,
	}

	controller.HandlePipelineCreated(context.Background(), evt, nil)

	if _, exists := controller.Pipelines[pipeline.Name]; !exists {
		t.Error("Expected pipeline to be in map after creation")
	}
}

func TestBasePipelineController_HandlePipelineUpdated(t *testing.T) {
	scheme := runtime.NewScheme()
	if err := v1alpha1.AddToScheme(scheme); err != nil {
		t.Fatalf("Failed to add v1alpha1 scheme: %v", err)
	}

	oldPipeline := &v1alpha1.Pipeline{
		ObjectMeta: metav1.ObjectMeta{
			Name: "test-pipeline",
		},
		Spec: v1alpha1.PipelineSpec{
			SchedulingDomain: v1alpha1.SchedulingDomainNova,
			Type:             v1alpha1.PipelineTypeFilterWeigher,
			Steps:            []v1alpha1.StepSpec{},
		},
	}

	newPipeline := oldPipeline.DeepCopy()
	newPipeline.Spec.Description = "Updated description"

	fakeClient := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(newPipeline).
		WithStatusSubresource(&v1alpha1.Pipeline{}).
		Build()

	controller := &BasePipelineController[mockPipeline]{
		Client:           fakeClient,
		SchedulingDomain: v1alpha1.SchedulingDomainNova,
		Initializer: &mockPipelineInitializer{
			pipelineType: v1alpha1.PipelineTypeFilterWeigher,
		},
		Pipelines:       make(map[string]mockPipeline),
		PipelineConfigs: make(map[string]v1alpha1.Pipeline),
	}

	evt := event.UpdateEvent{
		ObjectOld: oldPipeline,
		ObjectNew: newPipeline,
	}

	controller.HandlePipelineUpdated(context.Background(), evt, nil)

	if _, exists := controller.Pipelines[newPipeline.Name]; !exists {
		t.Error("Expected pipeline to be in map after update")
	}
}

func TestBasePipelineController_HandlePipelineDeleted(t *testing.T) {
	scheme := runtime.NewScheme()
	if err := v1alpha1.AddToScheme(scheme); err != nil {
		t.Fatalf("Failed to add v1alpha1 scheme: %v", err)
	}

	pipeline := &v1alpha1.Pipeline{
		ObjectMeta: metav1.ObjectMeta{
			Name: "test-pipeline",
		},
		Spec: v1alpha1.PipelineSpec{
			SchedulingDomain: v1alpha1.SchedulingDomainNova,
			Type:             v1alpha1.PipelineTypeFilterWeigher,
		},
	}

	controller := &BasePipelineController[mockPipeline]{
		Pipelines: map[string]mockPipeline{
			"test-pipeline": {name: "test-pipeline"},
		},
		PipelineConfigs: map[string]v1alpha1.Pipeline{
			"test-pipeline": *pipeline,
		},
	}

	evt := event.DeleteEvent{
		Object: pipeline,
	}

	controller.HandlePipelineDeleted(context.Background(), evt, nil)

	if _, exists := controller.Pipelines[pipeline.Name]; exists {
		t.Error("Expected pipeline to be removed from map after deletion")
	}
	if _, exists := controller.PipelineConfigs[pipeline.Name]; exists {
		t.Error("Expected pipeline config to be removed from map after deletion")
	}
}

func TestBasePipelineController_checkStepReady(t *testing.T) {
	scheme := runtime.NewScheme()
	if err := v1alpha1.AddToScheme(scheme); err != nil {
		t.Fatalf("Failed to add v1alpha1 scheme: %v", err)
	}

	tests := []struct {
		name        string
		step        v1alpha1.StepSpec
		knowledges  []v1alpha1.Knowledge
		expectError bool
	}{
		{
			name: "step with no knowledge dependencies",
			step: v1alpha1.StepSpec{
				Type:       v1alpha1.StepTypeFilter,
				Impl:       "test-filter",
				Knowledges: []corev1.ObjectReference{},
			},
			knowledges:  []v1alpha1.Knowledge{},
			expectError: false,
		},
		{
			name: "step with ready knowledge",
			step: v1alpha1.StepSpec{
				Type: v1alpha1.StepTypeFilter,
				Impl: "test-filter",
				Knowledges: []corev1.ObjectReference{
					{Name: "ready-knowledge", Namespace: "default"},
				},
			},
			knowledges: []v1alpha1.Knowledge{
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "ready-knowledge",
						Namespace: "default",
					},
					Status: v1alpha1.KnowledgeStatus{
						RawLength: 10,
					},
				},
			},
			expectError: false,
		},
		{
			name: "step with knowledge in error state",
			step: v1alpha1.StepSpec{
				Type: v1alpha1.StepTypeFilter,
				Impl: "test-filter",
				Knowledges: []corev1.ObjectReference{
					{Name: "error-knowledge", Namespace: "default"},
				},
			},
			knowledges: []v1alpha1.Knowledge{
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "error-knowledge",
						Namespace: "default",
					},
					Status: v1alpha1.KnowledgeStatus{
						Conditions: []metav1.Condition{
							{
								Type:   v1alpha1.KnowledgeConditionError,
								Status: metav1.ConditionTrue,
							},
						},
					},
				},
			},
			expectError: true,
		},
		{
			name: "step with knowledge with no data",
			step: v1alpha1.StepSpec{
				Type: v1alpha1.StepTypeFilter,
				Impl: "test-filter",
				Knowledges: []corev1.ObjectReference{
					{Name: "no-data-knowledge", Namespace: "default"},
				},
			},
			knowledges: []v1alpha1.Knowledge{
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "no-data-knowledge",
						Namespace: "default",
					},
					Status: v1alpha1.KnowledgeStatus{
						RawLength: 0,
					},
				},
			},
			expectError: true,
		},
		{
			name: "step with missing knowledge",
			step: v1alpha1.StepSpec{
				Type: v1alpha1.StepTypeFilter,
				Impl: "test-filter",
				Knowledges: []corev1.ObjectReference{
					{Name: "missing-knowledge", Namespace: "default"},
				},
			},
			knowledges:  []v1alpha1.Knowledge{},
			expectError: true,
		},
		{
			name: "step with multiple knowledges, all ready",
			step: v1alpha1.StepSpec{
				Type: v1alpha1.StepTypeFilter,
				Impl: "test-filter",
				Knowledges: []corev1.ObjectReference{
					{Name: "knowledge-1", Namespace: "default"},
					{Name: "knowledge-2", Namespace: "default"},
				},
			},
			knowledges: []v1alpha1.Knowledge{
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "knowledge-1",
						Namespace: "default",
					},
					Status: v1alpha1.KnowledgeStatus{
						RawLength: 10,
					},
				},
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "knowledge-2",
						Namespace: "default",
					},
					Status: v1alpha1.KnowledgeStatus{
						RawLength: 5,
					},
				},
			},
			expectError: false,
		},
		{
			name: "step with multiple knowledges, some not ready",
			step: v1alpha1.StepSpec{
				Type: v1alpha1.StepTypeFilter,
				Impl: "test-filter",
				Knowledges: []corev1.ObjectReference{
					{Name: "ready-knowledge", Namespace: "default"},
					{Name: "not-ready-knowledge", Namespace: "default"},
				},
			},
			knowledges: []v1alpha1.Knowledge{
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "ready-knowledge",
						Namespace: "default",
					},
					Status: v1alpha1.KnowledgeStatus{
						RawLength: 10,
					},
				},
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "not-ready-knowledge",
						Namespace: "default",
					},
					Status: v1alpha1.KnowledgeStatus{
						RawLength: 0,
					},
				},
			},
			expectError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			objects := make([]client.Object, len(tt.knowledges))
			for i := range tt.knowledges {
				objects[i] = &tt.knowledges[i]
			}

			fakeClient := fake.NewClientBuilder().
				WithScheme(scheme).
				WithObjects(objects...).
				Build()

			controller := &BasePipelineController[mockPipeline]{
				Client: fakeClient,
			}

			err := controller.checkStepReady(context.Background(), &tt.step)

			if tt.expectError && err == nil {
				t.Error("Expected error but got none")
			}
			if !tt.expectError && err != nil {
				t.Errorf("Expected no error but got: %v", err)
			}
		})
	}
}

func TestBasePipelineController_handleKnowledgeChange(t *testing.T) {
	scheme := runtime.NewScheme()
	if err := v1alpha1.AddToScheme(scheme); err != nil {
		t.Fatalf("Failed to add v1alpha1 scheme: %v", err)
	}

	tests := []struct {
		name              string
		knowledge         *v1alpha1.Knowledge
		pipelines         []v1alpha1.Pipeline
		schedulingDomain  v1alpha1.SchedulingDomain
		expectReEvaluated []string
	}{
		{
			name: "knowledge change triggers dependent pipeline re-evaluation",
			knowledge: &v1alpha1.Knowledge{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-knowledge",
					Namespace: "default",
				},
				Spec: v1alpha1.KnowledgeSpec{
					SchedulingDomain: v1alpha1.SchedulingDomainNova,
				},
				Status: v1alpha1.KnowledgeStatus{
					RawLength: 10,
				},
			},
			pipelines: []v1alpha1.Pipeline{
				{
					ObjectMeta: metav1.ObjectMeta{
						Name: "dependent-pipeline",
					},
					Spec: v1alpha1.PipelineSpec{
						SchedulingDomain: v1alpha1.SchedulingDomainNova,
						Type:             v1alpha1.PipelineTypeFilterWeigher,
						Steps: []v1alpha1.StepSpec{
							{
								Type: v1alpha1.StepTypeFilter,
								Impl: "test-filter",
								Knowledges: []corev1.ObjectReference{
									{Name: "test-knowledge", Namespace: "default"},
								},
							},
						},
					},
				},
				{
					ObjectMeta: metav1.ObjectMeta{
						Name: "independent-pipeline",
					},
					Spec: v1alpha1.PipelineSpec{
						SchedulingDomain: v1alpha1.SchedulingDomainNova,
						Type:             v1alpha1.PipelineTypeFilterWeigher,
						Steps: []v1alpha1.StepSpec{
							{
								Type: v1alpha1.StepTypeFilter,
								Impl: "test-filter",
								Knowledges: []corev1.ObjectReference{
									{Name: "other-knowledge", Namespace: "default"},
								},
							},
						},
					},
				},
			},
			schedulingDomain:  v1alpha1.SchedulingDomainNova,
			expectReEvaluated: []string{"dependent-pipeline"},
		},
		{
			name: "knowledge change in different scheduling domain",
			knowledge: &v1alpha1.Knowledge{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-knowledge",
					Namespace: "default",
				},
				Spec: v1alpha1.KnowledgeSpec{
					SchedulingDomain: v1alpha1.SchedulingDomainCinder,
				},
			},
			pipelines: []v1alpha1.Pipeline{
				{
					ObjectMeta: metav1.ObjectMeta{
						Name: "nova-pipeline",
					},
					Spec: v1alpha1.PipelineSpec{
						SchedulingDomain: v1alpha1.SchedulingDomainNova,
						Type:             v1alpha1.PipelineTypeFilterWeigher,
						Steps: []v1alpha1.StepSpec{
							{
								Type: v1alpha1.StepTypeFilter,
								Impl: "test-filter",
								Knowledges: []corev1.ObjectReference{
									{Name: "test-knowledge", Namespace: "default"},
								},
							},
						},
					},
				},
			},
			schedulingDomain:  v1alpha1.SchedulingDomainNova,
			expectReEvaluated: []string{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			objects := []client.Object{tt.knowledge}
			for i := range tt.pipelines {
				objects = append(objects, &tt.pipelines[i])
			}

			fakeClient := fake.NewClientBuilder().
				WithScheme(scheme).
				WithObjects(objects...).
				WithStatusSubresource(&v1alpha1.Pipeline{}, &v1alpha1.Knowledge{}).
				Build()

			controller := &BasePipelineController[mockPipeline]{
				Client:           fakeClient,
				SchedulingDomain: tt.schedulingDomain,
				Initializer: &mockPipelineInitializer{
					pipelineType: v1alpha1.PipelineTypeFilterWeigher,
				},
				Pipelines:       make(map[string]mockPipeline),
				PipelineConfigs: make(map[string]v1alpha1.Pipeline),
			}

			controller.handleKnowledgeChange(context.Background(), tt.knowledge, nil)

			// Verify expected pipelines were re-evaluated by checking if they're in the map
			for _, expectedName := range tt.expectReEvaluated {
				if _, exists := controller.Pipelines[expectedName]; !exists {
					t.Errorf("Expected pipeline %s to be re-evaluated", expectedName)
				}
			}
		})
	}
}

func TestBasePipelineController_HandleKnowledgeCreated(t *testing.T) {
	scheme := runtime.NewScheme()
	if err := v1alpha1.AddToScheme(scheme); err != nil {
		t.Fatalf("Failed to add v1alpha1 scheme: %v", err)
	}

	knowledge := &v1alpha1.Knowledge{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-knowledge",
			Namespace: "default",
		},
		Spec: v1alpha1.KnowledgeSpec{
			SchedulingDomain: v1alpha1.SchedulingDomainNova,
		},
		Status: v1alpha1.KnowledgeStatus{
			RawLength: 10,
		},
	}

	pipeline := &v1alpha1.Pipeline{
		ObjectMeta: metav1.ObjectMeta{
			Name: "test-pipeline",
		},
		Spec: v1alpha1.PipelineSpec{
			SchedulingDomain: v1alpha1.SchedulingDomainNova,
			Type:             v1alpha1.PipelineTypeFilterWeigher,
			Steps: []v1alpha1.StepSpec{
				{
					Type: v1alpha1.StepTypeFilter,
					Impl: "test-filter",
					Knowledges: []corev1.ObjectReference{
						{Name: "test-knowledge", Namespace: "default"},
					},
				},
			},
		},
	}

	fakeClient := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(knowledge, pipeline).
		WithStatusSubresource(&v1alpha1.Pipeline{}, &v1alpha1.Knowledge{}).
		Build()

	controller := &BasePipelineController[mockPipeline]{
		Client:           fakeClient,
		SchedulingDomain: v1alpha1.SchedulingDomainNova,
		Initializer: &mockPipelineInitializer{
			pipelineType: v1alpha1.PipelineTypeFilterWeigher,
		},
		Pipelines:       make(map[string]mockPipeline),
		PipelineConfigs: make(map[string]v1alpha1.Pipeline),
	}

	evt := event.CreateEvent{
		Object: knowledge,
	}

	controller.HandleKnowledgeCreated(context.Background(), evt, nil)

	// Pipeline should be re-evaluated and added to map
	if _, exists := controller.Pipelines[pipeline.Name]; !exists {
		t.Error("Expected pipeline to be re-evaluated after knowledge creation")
	}
}

func TestBasePipelineController_HandleKnowledgeUpdated(t *testing.T) {
	scheme := runtime.NewScheme()
	if err := v1alpha1.AddToScheme(scheme); err != nil {
		t.Fatalf("Failed to add v1alpha1 scheme: %v", err)
	}

	tests := []struct {
		name             string
		oldKnowledge     *v1alpha1.Knowledge
		newKnowledge     *v1alpha1.Knowledge
		expectReEvaluate bool
	}{
		{
			name: "error state changed",
			oldKnowledge: &v1alpha1.Knowledge{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-knowledge",
					Namespace: "default",
				},
				Spec: v1alpha1.KnowledgeSpec{
					SchedulingDomain: v1alpha1.SchedulingDomainNova,
				},
				Status: v1alpha1.KnowledgeStatus{
					Conditions: []metav1.Condition{
						{
							Type:   v1alpha1.KnowledgeConditionError,
							Status: metav1.ConditionTrue,
						},
					},
				},
			},
			newKnowledge: &v1alpha1.Knowledge{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-knowledge",
					Namespace: "default",
				},
				Spec: v1alpha1.KnowledgeSpec{
					SchedulingDomain: v1alpha1.SchedulingDomainNova,
				},
				Status: v1alpha1.KnowledgeStatus{
					RawLength: 10,
				},
			},
			expectReEvaluate: true,
		},
		{
			name: "data became available",
			oldKnowledge: &v1alpha1.Knowledge{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-knowledge",
					Namespace: "default",
				},
				Spec: v1alpha1.KnowledgeSpec{
					SchedulingDomain: v1alpha1.SchedulingDomainNova,
				},
				Status: v1alpha1.KnowledgeStatus{
					RawLength: 0,
				},
			},
			newKnowledge: &v1alpha1.Knowledge{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-knowledge",
					Namespace: "default",
				},
				Spec: v1alpha1.KnowledgeSpec{
					SchedulingDomain: v1alpha1.SchedulingDomainNova,
				},
				Status: v1alpha1.KnowledgeStatus{
					RawLength: 10,
				},
			},
			expectReEvaluate: true,
		},
		{
			name: "no relevant change",
			oldKnowledge: &v1alpha1.Knowledge{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-knowledge",
					Namespace: "default",
				},
				Spec: v1alpha1.KnowledgeSpec{
					SchedulingDomain: v1alpha1.SchedulingDomainNova,
				},
				Status: v1alpha1.KnowledgeStatus{
					RawLength: 10,
				},
			},
			newKnowledge: &v1alpha1.Knowledge{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-knowledge",
					Namespace: "default",
				},
				Spec: v1alpha1.KnowledgeSpec{
					SchedulingDomain: v1alpha1.SchedulingDomainNova,
				},
				Status: v1alpha1.KnowledgeStatus{
					RawLength: 15,
				},
			},
			expectReEvaluate: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			pipeline := &v1alpha1.Pipeline{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-pipeline",
				},
				Spec: v1alpha1.PipelineSpec{
					SchedulingDomain: v1alpha1.SchedulingDomainNova,
					Type:             v1alpha1.PipelineTypeFilterWeigher,
					Steps: []v1alpha1.StepSpec{
						{
							Type: v1alpha1.StepTypeFilter,
							Impl: "test-filter",
							Knowledges: []corev1.ObjectReference{
								{Name: "test-knowledge", Namespace: "default"},
							},
						},
					},
				},
			}

			fakeClient := fake.NewClientBuilder().
				WithScheme(scheme).
				WithObjects(tt.newKnowledge, pipeline).
				WithStatusSubresource(&v1alpha1.Pipeline{}, &v1alpha1.Knowledge{}).
				Build()

			controller := &BasePipelineController[mockPipeline]{
				Client:           fakeClient,
				SchedulingDomain: v1alpha1.SchedulingDomainNova,
				Initializer: &mockPipelineInitializer{
					pipelineType: v1alpha1.PipelineTypeFilterWeigher,
				},
				Pipelines:       make(map[string]mockPipeline),
				PipelineConfigs: make(map[string]v1alpha1.Pipeline),
			}

			evt := event.UpdateEvent{
				ObjectOld: tt.oldKnowledge,
				ObjectNew: tt.newKnowledge,
			}

			controller.HandleKnowledgeUpdated(context.Background(), evt, nil)

			_, exists := controller.Pipelines[pipeline.Name]
			if tt.expectReEvaluate && !exists {
				t.Error("Expected pipeline to be re-evaluated")
			}
			if !tt.expectReEvaluate && exists {
				t.Error("Expected pipeline not to be re-evaluated")
			}
		})
	}
}

func TestBasePipelineController_HandleKnowledgeDeleted(t *testing.T) {
	scheme := runtime.NewScheme()
	if err := v1alpha1.AddToScheme(scheme); err != nil {
		t.Fatalf("Failed to add v1alpha1 scheme: %v", err)
	}

	knowledge := &v1alpha1.Knowledge{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-knowledge",
			Namespace: "default",
		},
		Spec: v1alpha1.KnowledgeSpec{
			SchedulingDomain: v1alpha1.SchedulingDomainNova,
		},
	}

	pipeline := &v1alpha1.Pipeline{
		ObjectMeta: metav1.ObjectMeta{
			Name: "test-pipeline",
		},
		Spec: v1alpha1.PipelineSpec{
			SchedulingDomain: v1alpha1.SchedulingDomainNova,
			Type:             v1alpha1.PipelineTypeFilterWeigher,
			Steps: []v1alpha1.StepSpec{
				{
					Type:      v1alpha1.StepTypeFilter,
					Impl:      "test-filter",
					Mandatory: true,
					Knowledges: []corev1.ObjectReference{
						{Name: "test-knowledge", Namespace: "default"},
					},
				},
			},
		},
	}

	fakeClient := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(pipeline).
		WithStatusSubresource(&v1alpha1.Pipeline{}).
		Build()

	controller := &BasePipelineController[mockPipeline]{
		Client:           fakeClient,
		SchedulingDomain: v1alpha1.SchedulingDomainNova,
		Initializer: &mockPipelineInitializer{
			pipelineType: v1alpha1.PipelineTypeFilterWeigher,
		},
		Pipelines: map[string]mockPipeline{
			"test-pipeline": {name: "test-pipeline"},
		},
		PipelineConfigs: make(map[string]v1alpha1.Pipeline),
	}

	evt := event.DeleteEvent{
		Object: knowledge,
	}

	controller.HandleKnowledgeDeleted(context.Background(), evt, nil)

	// When knowledge is deleted, the pipeline is re-evaluated.
	// Since the knowledge is now missing and the step is mandatory,
	// the pipeline should be removed from the map.
	if _, exists := controller.Pipelines[pipeline.Name]; exists {
		t.Error("Expected pipeline to be removed after knowledge deletion due to mandatory step")
	}
}
